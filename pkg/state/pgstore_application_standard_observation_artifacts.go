package state

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func qualifyStandardArtifactsTx(ctx context.Context, tx pgx.Tx, r ApplicationStandardConsumerRoster, q standardApplicationQualification) (standardApplicationQualification, error) {
	rows, err := sqlc.New().ListApplicationStandardObservationArtifacts(ctx, tx, mustPgUUID(r.AppID))
	if err != nil {
		return standardApplicationQualification{reason: "artifact_approval_stale"}, standardObservationArtifactError(registryVerificationError(err))
	}
	if len(rows) == 0 {
		return standardApplicationQualification{reason: "artifact_inventory_empty"}, nil
	}
	for _, row := range rows {
		evidence, err := freshRuntimeScanTx(ctx, tx, r.AccountID, r.AppID, pgUUIDString(row.ID))
		if err != nil {
			return standardApplicationQualification{reason: "artifact_approval_stale"}, standardObservationArtifactError(err)
		}
		artifact := standardObservationArtifact(row.Input, evidence)
		if artifact.reason != "" {
			return artifact, nil
		}
		q.restrict(artifact.until)
		q, err = qualifyStandardSnapshotsTx(ctx, tx, r, pgUUIDString(row.ID), row.Input, q)
		if err != nil || q.reason != "" {
			return q, err
		}
	}
	return q, nil
}

func qualifyStandardSnapshotsTx(ctx context.Context, tx pgx.Tx, r ApplicationStandardConsumerRoster, depID string, current []byte, q standardApplicationQualification) (standardApplicationQualification, error) {
	rows, err := sqlc.New().ListApplicationStandardObservationSnapshots(ctx, tx, sqlc.ListApplicationStandardObservationSnapshotsParams{DeploymentID: mustPgUUID(depID), HeartbeatSeconds: DefaultHeartbeatStaleness.Seconds()})
	if err != nil {
		return q, err
	}
	for _, row := range rows {
		if !row.CatalogMatches || !row.HeartbeatUntil.Valid {
			return standardApplicationQualification{reason: "snapshot_observation_pending"}, nil
		}
		record := ApplicationStandardSnapshotCaptureRecord{inputs: row.InputSnapshot, ExpectedState: row.ExpectedState.String, CreatedAt: row.CreatedAt.Time, ReceivedAt: row.ReceivedAt.Time}
		record, valid := decodeStandardObservationSnapshotHistory(record, row.GrantData, row.Acknowledgment)
		if !valid {
			return standardApplicationQualification{reason: "snapshot_observation_pending"}, nil
		}
		if reason := standardObservationSnapshot(record, current, r); reason != "" {
			return standardApplicationQualification{reason: reason}, nil
		}
		q.restrict(row.HeartbeatUntil.Time)
	}
	return q, nil
}
