package state

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ EnvironmentQualificationSmokeReceiptStore = (*PgStore)(nil)

func qualificationSmokeReceiptFromSQL(row sqlc.EnvironmentQualificationSmokeReceipt) EnvironmentQualificationSmokeReceipt {
	return EnvironmentQualificationSmokeReceipt{
		RequestID:         pgUUIDString(row.RequestID),
		Attempt:           row.Attempt,
		GraphID:           pgUUIDString(row.GraphID),
		CaptureInstanceID: pgUUIDString(row.CaptureInstanceID),
		InstanceID:        pgUUIDString(row.InstanceID),
		Resource:          row.Resource,
		PolicyID:          row.PolicyID,
		PolicySHA256:      row.PolicySha256,
		ResultSHA256:      row.ResultSha256,
		RecordedAt:        row.RecordedAt.Time,
	}
}

func (s *PgStore) EnvironmentQualificationSmokeReceipt(ctx context.Context, requestID string, attempt int64) (EnvironmentQualificationSmokeReceipt, error) {
	if !qualificationRecoveryUUIDValid(requestID) || attempt < 1 {
		return EnvironmentQualificationSmokeReceipt{}, ErrInvalidArgument
	}
	row, err := sqlc.New().EnvironmentQualificationSmokeReceipt(ctx, s.pool, sqlc.EnvironmentQualificationSmokeReceiptParams{
		RequestID: mustPgUUID(requestID), Attempt: attempt,
	})
	if err != nil {
		return EnvironmentQualificationSmokeReceipt{}, mapErr(err)
	}
	return qualificationSmokeReceiptFromSQL(row), nil
}

func (s *PgStore) RecordEnvironmentQualificationSmokeReceipt(ctx context.Context,
	claimed EnvironmentWorkloadQualificationRequest, evidence EnvironmentQualificationSmokeEvidence) (EnvironmentQualificationSmokeReceipt, error) {
	var zero EnvironmentQualificationSmokeReceipt
	if !qualificationRecoveryUUIDValid(claimed.ID) || !qualificationRecoveryUUIDValid(claimed.GraphID) ||
		!qualificationRecoveryUUIDValid(claimed.ReservedInstanceID) || claimed.Attempt < 1 {
		return zero, ErrInvalidArgument
	}
	if err := evidence.ValidateFor(claimed, evidence.InstanceID); err != nil {
		return zero, err
	}
	q := sqlc.New()
	prior, err := q.EnvironmentQualificationSmokeReceipt(ctx, s.pool, sqlc.EnvironmentQualificationSmokeReceiptParams{
		RequestID: mustPgUUID(claimed.ID), Attempt: claimed.Attempt,
	})
	if err == nil {
		receipt := qualificationSmokeReceiptFromSQL(prior)
		storedRestore, restoreErr := q.EnvironmentQualificationRestoreReceipt(ctx, s.pool, sqlc.EnvironmentQualificationRestoreReceiptParams{
			RequestID: mustPgUUID(claimed.ID), Attempt: claimed.Attempt,
		})
		if errors.Is(restoreErr, pgx.ErrNoRows) {
			return zero, ErrConflict
		}
		if restoreErr != nil {
			return zero, mapErr(restoreErr)
		}
		restore, restoreErr := qualificationRestoreReceiptFromSQL(storedRestore)
		if restoreErr != nil {
			return zero, restoreErr
		}
		if !qualificationSmokeReceiptMatchesRequest(receipt, claimed, restore) || receipt.Resource != evidence.Resource ||
			receipt.InstanceID != evidence.InstanceID || receipt.PolicyID != evidence.PolicyID ||
			receipt.PolicySHA256 != evidence.PolicySHA256 || receipt.ResultSHA256 != evidence.ResultSHA256 {
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
	if !qualificationClaimIdentityMatches(current, claimed) || !qualificationLeaseMatches(current, claimed, time.Now()) {
		return zero, ErrConflict
	}
	if _, err := q.SetEnvironmentWorkloadQualificationContext(ctx, tx, claimed.LeaseToken); err != nil {
		return zero, mapErr(err)
	}
	originalRow, err := q.LockEnvironmentQualificationExecution(ctx, tx, mustPgUUID(current.ReservedInstanceID))
	if errors.Is(err, pgx.ErrNoRows) {
		return zero, ErrConflict
	}
	if err != nil {
		return zero, mapErr(err)
	}
	original, err := qualificationExecutionFromSQL(originalRow)
	if err != nil {
		return zero, err
	}
	captureRow, err := q.EnvironmentQualificationSnapshotReceipt(ctx, tx, mustPgUUID(current.ReservedInstanceID))
	if errors.Is(err, pgx.ErrNoRows) {
		return zero, ErrConflict
	}
	if err != nil {
		return zero, mapErr(err)
	}
	capture, err := qualificationSnapshotReceiptFromSQL(captureRow)
	if err != nil {
		return zero, err
	}
	restoredRow, err := q.EnvironmentQualificationRestoreReceipt(ctx, tx, sqlc.EnvironmentQualificationRestoreReceiptParams{
		RequestID: row.ID, Attempt: row.Attempt,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return zero, ErrConflict
	}
	if err != nil {
		return zero, mapErr(err)
	}
	restored, err := qualificationRestoreReceiptFromSQL(restoredRow)
	if err != nil {
		return zero, err
	}
	targetRow, err := q.LockEnvironmentQualificationExecution(ctx, tx, mustPgUUID(restored.InstanceID))
	if errors.Is(err, pgx.ErrNoRows) {
		return zero, ErrConflict
	}
	if err != nil {
		return zero, mapErr(err)
	}
	target, err := qualificationExecutionFromSQL(targetRow)
	if err != nil {
		return zero, err
	}
	instanceRow, err := q.LockEnvironmentQualificationRuntimeInstance(ctx, tx, mustPgUUID(restored.InstanceID))
	if errors.Is(err, pgx.ErrNoRows) {
		return zero, ErrConflict
	}
	if err != nil {
		return zero, mapErr(err)
	}
	instance := qualificationInstanceFromSQL(instanceRow)
	if instance.State != string(StateStopped) || instance.WakeID != target.Execution.WakeID ||
		target.Execution.CaptureInstanceID != current.ReservedInstanceID ||
		target.Execution.InstanceID != evidence.InstanceID ||
		!qualificationRestoreReceiptValid(current, original, capture, target, restored.Inputs) {
		return zero, ErrConflict
	}
	config, err := q.InstanceRuntimeConfigReceipt(ctx, tx, mustPgUUID(restored.InstanceID))
	if errors.Is(err, pgx.ErrNoRows) {
		return zero, ErrConflict
	}
	if err != nil {
		return zero, mapErr(err)
	}
	inputs, err := runtimeConfigInputsFromSQL(config.Scope, config.BoundaryAt, config.Variables, config.SecretVersions,
		config.SecretRefs, config.SidecarSecretVersions, config.AllSecrets)
	if err != nil || pgUUIDString(config.WakeID) != target.Execution.WakeID || !runtimeConfigInputsPostgresEqual(inputs, restored.Inputs) {
		return zero, ErrConflict
	}
	captureFresh, err := readRuntimeConfigInputsFresh(ctx, tx, current.AppID, capture.Inputs)
	if err != nil {
		return zero, err
	}
	restoreFresh, err := readRuntimeConfigInputsFresh(ctx, tx, current.AppID, restored.Inputs)
	if err != nil {
		return zero, err
	}
	if !captureFresh || !restoreFresh {
		return zero, ErrConflict
	}
	stored, err := q.RecordEnvironmentQualificationSmokeReceipt(ctx, tx, sqlc.RecordEnvironmentQualificationSmokeReceiptParams{
		RequestID: row.ID, Attempt: row.Attempt, GraphID: row.GraphID,
		CaptureInstanceID: mustPgUUID(current.ReservedInstanceID), InstanceID: mustPgUUID(restored.InstanceID),
		Resource: evidence.Resource, PolicyID: evidence.PolicyID, PolicySha256: evidence.PolicySHA256, ResultSha256: evidence.ResultSHA256,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		stored, err = q.EnvironmentQualificationSmokeReceipt(ctx, tx, sqlc.EnvironmentQualificationSmokeReceiptParams{
			RequestID: row.ID, Attempt: row.Attempt,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return zero, ErrConflict
		}
	}
	if err != nil {
		return zero, mapErr(err)
	}
	receipt := qualificationSmokeReceiptFromSQL(stored)
	if !qualificationSmokeReceiptMatchesRequest(receipt, claimed, restored) || receipt.Resource != evidence.Resource ||
		receipt.PolicyID != evidence.PolicyID || receipt.PolicySHA256 != evidence.PolicySHA256 || receipt.ResultSHA256 != evidence.ResultSHA256 {
		return zero, ErrConflict
	}
	if err := tx.Commit(ctx); err != nil {
		return zero, err
	}
	return receipt, nil
}
