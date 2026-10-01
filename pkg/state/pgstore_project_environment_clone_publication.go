package state

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) ProjectEnvironmentCloneTargetOperation(ctx context.Context, accountID, projectID, environment string) (ProjectEnvironmentCloneOperation, error) {
	id, err := new(sqlc.Queries).ReadProjectEnvironmentCloneTargetOperationID(ctx, s.pool, sqlc.ReadProjectEnvironmentCloneTargetOperationIDParams{AccountID: mustPgUUID(accountID), ProjectID: mustPgUUID(projectID), Environment: environment})
	if err != nil {
		return ProjectEnvironmentCloneOperation{}, mapErr(err)
	}
	return s.ProjectEnvironmentCloneOperationByID(ctx, accountID, projectID, id)
}

func cloneWorkloadProofsTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) ([]projectCloneWorkloadProof, error) {
	q := new(sqlc.Queries)
	records, err := cloneWorkloadRecordsDB(ctx, tx, op.AccountID, op.ProjectID, op.ID)
	if err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return nil, nil
	}
	if _, err := q.LockProjectEnvironmentCloneApps(ctx, tx, sqlc.LockProjectEnvironmentCloneAppsParams{AccountID: mustPgUUID(op.AccountID), ProjectID: mustPgUUID(op.ProjectID)}); err != nil {
		return nil, mapErr(err)
	}
	if _, err := q.LockProjectEnvironmentCloneTargetDeployments(ctx, tx, mustPgUUID(op.ID)); err != nil {
		return nil, mapErr(err)
	}
	if _, err := q.LockProjectEnvironmentCloneTargetSidecarLayers(ctx, tx, mustPgUUID(op.ID)); err != nil {
		return nil, mapErr(err)
	}
	if _, err := q.LockProjectEnvironmentCloneTargetSidecarSignals(ctx, tx, mustPgUUID(op.ID)); err != nil {
		return nil, mapErr(err)
	}
	var proofs []projectCloneWorkloadProof
	for _, record := range records {
		if record.TargetDeploymentID == "" {
			return nil, ErrConflict
		}
		d, err := deploymentByIDDB(ctx, tx, record.TargetDeploymentID)
		if err != nil {
			return nil, err
		}
		p := projectCloneWorkloadProof{view: record.ProjectEnvironmentCloneWorkload, live: d.AppID == record.AppID && d.Scope == op.TargetEnvironment && d.Status == DeployLive}
		head, err := q.ReadProjectEnvironmentCloneTargetSettings(ctx, tx, sqlc.ReadProjectEnvironmentCloneTargetSettingsParams{AccountID: mustPgUUID(op.AccountID), ProjectID: mustPgUUID(op.ProjectID), AppID: mustPgUUID(record.AppID), Environment: op.TargetEnvironment})
		if err != nil {
			return nil, mapErr(err)
		}
		p.headHash = head.ConfigHash
		pin, err := q.ReadProjectEnvironmentCloneDeployedSettings(ctx, tx, mustPgUUID(d.ID))
		if err != nil {
			return nil, mapErr(err)
		}
		p.pinHash = pin.ConfigHash
		var layers []DeploymentSidecarLayer
		rawLayers, err := q.ReadProjectEnvironmentCloneSidecarLayers(ctx, tx, mustPgUUID(d.ID))
		if err != nil {
			return nil, mapErr(err)
		}
		for _, raw := range rawLayers {
			var layer DeploymentSidecarLayer
			if err := json.Unmarshal(raw, &layer); err != nil {
				return nil, ErrConflict
			}
			layers = append(layers, layer)
		}
		signals := map[string]string{}
		rows, err := q.ReadProjectEnvironmentCloneSidecarSignals(ctx, tx, mustPgUUID(d.ID))
		if err != nil {
			return nil, mapErr(err)
		}
		for _, row := range rows {
			signals[row.SidecarName] = row.Signal
		}
		// The legacy Deployment projection omits reload opt-ins and coalesces
		// nullable fields. Compare the same lossless projection used at capture.
		rawArtifact, err := q.ReadProjectEnvironmentCloneTargetArtifact(ctx, tx, mustPgUUID(d.ID))
		if err != nil {
			return nil, mapErr(err)
		}
		var artifact projectCloneArtifact
		if err := json.Unmarshal(rawArtifact, &artifact); err != nil {
			return nil, ErrConflict
		}
		p.artifactHash, err = cloneWorkloadTargetArtifactHash(record, d, layers, signals, artifact)
		if err != nil {
			return nil, err
		}
		proofs = append(proofs, p)
	}
	return proofs, nil
}

func verifyClonePublicationTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, resources []ProjectEnvironmentCloneResource) error {
	// A coordinator retry may have waited for VM readiness after materializing
	// configuration. Recheck its original environment and desired heads before
	// acquiring app/resource locks, and again at the final graph transaction.
	if _, err := replayCloneMaterializationTx(ctx, tx, op); err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	// Take the flag writer's environment lock before app/resource locks.
	if _, err := readCloneFeatureFlagsTx(ctx, tx, op.AccountID, op.ProjectID, op.TargetEnvironment); err != nil {
		return err
	}
	rows, err := new(sqlc.Queries).ReadProjectEnvironmentCloneObjectCopyProofs(ctx, tx, mustPgUUID(op.ID))
	if err != nil {
		return mapErr(err)
	}
	proofs := make([]projectCloneObjectCopyProof, len(rows))
	for i, row := range rows {
		proofs[i] = projectCloneObjectCopyProof{sourceID: row.SourceBucketID, targetID: row.TargetBucketID, hash: row.ManifestHash,
			capture: row.CapturedAtExact, objectCount: int(row.ObjectCount), entryCount: int(row.EntryCount), verifiedCount: int(row.VerifiedCount)}
	}
	if err := validateCloneObjectCopyProofs(resources, proofs); err != nil {
		return err
	}
	workloads, err := cloneWorkloadProofsTx(ctx, tx, op)
	if err != nil {
		return err
	}
	if err := validateCloneWorkloadProofs(resources, workloads); err != nil {
		return err
	}
	if err := verifyCloneValuePublicationTx(ctx, tx, op, resources); err != nil {
		return err
	}
	if err := verifyCloneScopedPolicyPublicationDB(ctx, tx, op); err != nil {
		return err
	}
	return verifyCloneProjectConfigTx(ctx, tx, op, resources)
}

func (s *PgStore) PublishProjectEnvironmentCloneReleaseSet(ctx context.Context, accountID, projectID, operationID string, revision int64, ttl int) (ProjectReleaseSet, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ProjectReleaseSet{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	op, err := lockCloneWorkloadOperationTx(ctx, tx, accountID, projectID, operationID)
	if err != nil {
		return ProjectReleaseSet{}, err
	}
	if op.Status == CloneOperationReady && op.TargetReleaseSetID != "" {
		active, err := activeProjectReleaseSetIDTx(ctx, tx, projectID, op.TargetEnvironment)
		if err != nil {
			return ProjectReleaseSet{}, err
		}
		if active != op.TargetReleaseSetID {
			return ProjectReleaseSet{}, ErrConflict
		}
		return readProjectReleaseSetTx(ctx, tx, accountID, projectID, op.TargetEnvironment, op.TargetReleaseSetID)
	}
	if op.Status != CloneOperationPublishing || op.Revision != revision || op.TargetReleaseSetID != "" {
		return ProjectReleaseSet{}, ErrConflict
	}
	if err := validateCloneResourceTransition(op, CloneOperationReady, op.Resources, op.ErrorCode); err != nil {
		return ProjectReleaseSet{}, err
	}
	if err := verifyClonePublicationTx(ctx, tx, op, op.Resources); err != nil {
		return ProjectReleaseSet{}, err
	}
	records, err := cloneWorkloadRecordsDB(ctx, tx, accountID, projectID, operationID)
	if err != nil {
		return ProjectReleaseSet{}, err
	}
	var members []ProjectReleaseMember
	for _, record := range records {
		members = append(members, ProjectReleaseMember{AppID: record.AppID, DeploymentID: record.TargetDeploymentID})
	}
	empty := ""
	release, err := s.publishProjectReleaseSetTx(ctx, tx, accountID, projectID, op.TargetEnvironment, ttl, members, &empty)
	if err != nil {
		return ProjectReleaseSet{}, err
	}
	count, err := new(sqlc.Queries).CompleteProjectEnvironmentClonePublication(ctx, tx, sqlc.CompleteProjectEnvironmentClonePublicationParams{OperationID: mustPgUUID(operationID), ReleaseID: mustPgUUID(release.ID), Revision: revision})
	if err != nil {
		return ProjectReleaseSet{}, mapErr(err)
	}
	if count != 1 {
		return ProjectReleaseSet{}, ErrConflict
	}
	if err := tx.Commit(ctx); err != nil {
		return ProjectReleaseSet{}, mapErr(err)
	}
	return release, nil
}
