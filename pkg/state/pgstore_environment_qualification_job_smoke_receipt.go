package state

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ EnvironmentQualificationJobSmokeReceiptStore = (*PgStore)(nil)

func qualificationJobSmokeReceiptFromSQL(row sqlc.EnvironmentQualificationJobSmokeReceipt) EnvironmentQualificationJobSmokeReceipt {
	return EnvironmentQualificationJobSmokeReceipt{RequestID: pgUUIDString(row.RequestID), Attempt: row.Attempt,
		GraphID: pgUUIDString(row.GraphID), InstanceID: pgUUIDString(row.InstanceID), Resource: row.Resource,
		PolicyID: row.PolicyID, PolicySHA256: row.PolicySha256, ResultSHA256: row.ResultSha256, RecordedAt: row.RecordedAt.Time}
}

func (s *PgStore) EnvironmentQualificationJobSmokeReceipt(ctx context.Context, requestID string, attempt int64) (EnvironmentQualificationJobSmokeReceipt, error) {
	if !qualificationRecoveryUUIDValid(requestID) || attempt < 1 {
		return EnvironmentQualificationJobSmokeReceipt{}, ErrInvalidArgument
	}
	row, err := sqlc.New().EnvironmentQualificationJobSmokeReceipt(ctx, s.pool, sqlc.EnvironmentQualificationJobSmokeReceiptParams{
		RequestID: mustPgUUID(requestID), Attempt: attempt,
	})
	if err != nil {
		return EnvironmentQualificationJobSmokeReceipt{}, mapErr(err)
	}
	return qualificationJobSmokeReceiptFromSQL(row), nil
}

func (s *PgStore) RecordEnvironmentQualificationJobSmokeReceipt(ctx context.Context,
	claimed EnvironmentWorkloadQualificationRequest, evidence EnvironmentQualificationJobSmokeEvidence) (EnvironmentQualificationJobSmokeReceipt, error) {
	var zero EnvironmentQualificationJobSmokeReceipt
	if !qualificationRecoveryUUIDValid(claimed.ID) || !qualificationRecoveryUUIDValid(claimed.GraphID) ||
		!qualificationRecoveryUUIDValid(claimed.ReservedInstanceID) || claimed.Attempt < 1 {
		return zero, ErrInvalidArgument
	}
	if err := evidence.ValidateFor(claimed, evidence.InstanceID); err != nil {
		return zero, err
	}
	q := sqlc.New()
	prior, err := q.EnvironmentQualificationJobSmokeReceipt(ctx, s.pool, sqlc.EnvironmentQualificationJobSmokeReceiptParams{
		RequestID: mustPgUUID(claimed.ID), Attempt: claimed.Attempt,
	})
	if err == nil {
		receipt := qualificationJobSmokeReceiptFromSQL(prior)
		if !qualificationJobSmokeReceiptMatchesEvidence(receipt, claimed, evidence) {
			return zero, ErrConflict
		}
		return receipt, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return zero, mapErr(err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return zero, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	row, err := s.qualificationCurrentTx(ctx, tx, claimed.ID)
	if err != nil {
		return zero, err
	}
	current := qualificationRequestFromSQL(row)
	if current.ExecutionMode != "job" || !qualificationClaimIdentityMatches(current, claimed) || !qualificationLeaseMatches(current, claimed, time.Now()) {
		return zero, ErrConflict
	}
	if _, err := q.SetEnvironmentWorkloadQualificationContext(ctx, tx, claimed.LeaseToken); err != nil {
		return zero, mapErr(err)
	}
	frameRow, err := q.LockEnvironmentQualificationExecution(ctx, tx, mustPgUUID(current.ReservedInstanceID))
	if err != nil {
		return zero, mapErr(err)
	}
	frame, err := qualificationExecutionFromSQL(frameRow)
	if err != nil || frame.Execution.RequestID != current.ID || frame.Execution.Attempt != current.Attempt ||
		frame.Execution.InstanceID != evidence.InstanceID || !frame.DispatchStarted || frame.RetiredAt == nil ||
		frame.Retirement == nil || frame.Retirement.Kind != QualificationNativeRetired {
		return zero, ErrConflict
	}
	configRow, err := q.EnvironmentQualificationConfigReceipt(ctx, tx, mustPgUUID(evidence.InstanceID))
	if err != nil || !qualificationConfigReceiptMatchesFrame(qualificationConfigReceiptFromSQL(configRow), current, frame.Execution) {
		return zero, ErrConflict
	}
	instance, err := q.LockEnvironmentQualificationRuntimeInstance(ctx, tx, mustPgUUID(evidence.InstanceID))
	if err != nil || State(instance.State) != StateStopped || pgUUIDString(instance.WakeID) != frame.Execution.WakeID {
		return zero, ErrConflict
	}
	stored, err := q.RecordEnvironmentQualificationJobSmokeReceipt(ctx, tx, sqlc.RecordEnvironmentQualificationJobSmokeReceiptParams{
		RequestID: mustPgUUID(current.ID), Attempt: current.Attempt, GraphID: mustPgUUID(current.GraphID), InstanceID: mustPgUUID(evidence.InstanceID),
		Resource: evidence.Resource, PolicyID: evidence.PolicyID, PolicySha256: evidence.PolicySHA256, ResultSha256: evidence.ResultSHA256,
		ExitCode: int32(evidence.ExitCode), ErrorClass: evidence.ErrorClass, Signal: int32(evidence.Signal),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return zero, ErrConflict
	}
	if err != nil {
		return zero, mapErr(err)
	}
	receipt := qualificationJobSmokeReceiptFromSQL(stored)
	if !qualificationJobSmokeReceiptMatchesEvidence(receipt, claimed, evidence) {
		return zero, ErrConflict
	}
	return receipt, mapErr(tx.Commit(ctx))
}

func qualificationJobSmokeReceiptMatchesEvidence(receipt EnvironmentQualificationJobSmokeReceipt,
	claimed EnvironmentWorkloadQualificationRequest, evidence EnvironmentQualificationJobSmokeEvidence) bool {
	return receipt.RequestID == claimed.ID && receipt.Attempt == claimed.Attempt && receipt.GraphID == claimed.GraphID &&
		receipt.InstanceID == evidence.InstanceID && receipt.Resource == evidence.Resource && receipt.PolicyID == evidence.PolicyID &&
		receipt.PolicySHA256 == evidence.PolicySHA256 && receipt.ResultSHA256 == evidence.ResultSHA256
}
