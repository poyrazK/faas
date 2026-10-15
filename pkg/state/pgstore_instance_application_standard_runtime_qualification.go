package state

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ InstanceApplicationStandardRuntimeQualificationStore = (*PgStore)(nil)

func (s *PgStore) GetInstanceApplicationStandardRuntimeQualification(ctx context.Context, orgID, appID, id string) (InstanceApplicationStandardRuntimeQualification, error) {
	if !validStandardResourceRead(orgID, appID) || !validStandardResourceRead(id, id) {
		return InstanceApplicationStandardRuntimeQualification{}, ErrInvalidArgument
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return InstanceApplicationStandardRuntimeQualification{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	r, err := readStandardConsumerRoster(ctx, tx, orgID, appID)
	if err != nil {
		return InstanceApplicationStandardRuntimeQualification{}, err
	}
	q, err := standardRuntimeQualification(r, id)
	if err != nil || q.Reason != "" {
		return q, err
	}
	return readStandardRuntimeQualificationTx(ctx, tx, q)
}

func readStandardRuntimeQualificationTx(ctx context.Context, tx pgx.Tx, q InstanceApplicationStandardRuntimeQualification) (InstanceApplicationStandardRuntimeQualification, error) {
	c, err := readStandardRuntimeQualificationCapture(ctx, tx, q.InstanceID)
	if err != nil {
		return standardRuntimeQualificationFailure(q, "native_receipt_stale", err)
	}
	r, err := readStandardRuntimeReceipt(ctx, tx, q.InstanceID)
	if err != nil {
		return standardRuntimeQualificationFailure(q, "native_receipt_stale", err)
	}
	current, err := sqlc.New().GetApplicationStandardRuntimeQualificationInput(ctx, tx, sqlc.GetApplicationStandardRuntimeQualificationInputParams{InstanceID: mustPgUUID(q.InstanceID), AppID: mustPgUUID(q.AppID), OrgID: mustPgUUID(q.OrgID)})
	if err != nil {
		return standardRuntimeQualificationFailure(q, "runtime_inputs_stale", standardRuntimeQualificationReadError(err))
	}
	q.Reason = standardRuntimeQualificationInputs(q, c, r, current)
	if q.Reason != "" {
		return q, nil
	}
	evidence, err := freshRuntimeScanTx(ctx, tx, c.AccountID, c.AppID, c.DeploymentID)
	if err != nil {
		return standardRuntimeQualificationFailure(q, "artifact_approval_stale", err)
	}
	deadline, err := standardNativeArtifactDeadline(c, evidence)
	if err != nil {
		return standardRuntimeQualificationFailure(q, "artifact_approval_stale", err)
	}
	now, err := sqlc.New().ArtifactEvidenceStorageTime(ctx, tx)
	if err != nil || !now.Valid {
		return q, errors.Join(ErrApplicationStandardRuntimeStale, err)
	}
	return standardRuntimeQualificationComplete(q, deadline, now.Time), nil
}

func readStandardRuntimeQualificationCapture(ctx context.Context, db sqlc.DBTX, id string) (InstanceApplicationStandardAdmission, error) {
	row, err := sqlc.New().GetInstanceApplicationStandardAdmission(ctx, db, mustPgUUID(id))
	if err != nil {
		return InstanceApplicationStandardAdmission{}, mapErr(err)
	}
	c, err := decodeInstanceStandardAdmission(id, row.InputSnapshot, row.CapturedAt.Time)
	c.NodeID, c.NativeInputHash = row.NodeID, row.NativeInputHash.String
	return c, err
}

func standardRuntimeQualificationFailure(q InstanceApplicationStandardRuntimeQualification, reason string, err error) (InstanceApplicationStandardRuntimeQualification, error) {
	if errors.Is(err, ErrApplicationStandardRuntimeBusy) || errors.Is(err, ErrApplicationStandardReviewBusy) {
		q.Reason = "runtime_inputs_busy"
		return q, nil
	}
	if errors.Is(err, ErrNotFound) || errors.Is(err, ErrApplicationStandardRuntimeStale) || errors.Is(err, ErrApplicationStandardsPending) || standardReviewArtifactEvidenceUnavailable(err) {
		q.Reason = reason
		return q, nil
	}
	return q, err
}

func standardRuntimeQualificationReadError(err error) error {
	var p *pgconn.PgError
	if errors.As(err, &p) && (p.Code == "55P03" || p.Code == "40001") {
		return ErrApplicationStandardRuntimeBusy
	}
	return registryVerificationError(err)
}
