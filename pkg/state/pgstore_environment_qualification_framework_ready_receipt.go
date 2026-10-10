package state

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ EnvironmentQualificationFrameworkReadyReceiptStore = (*PgStore)(nil)

func qualificationFrameworkReadyReceiptFromSQL(row sqlc.EnvironmentQualificationFrameworkReadyReceipt) EnvironmentQualificationFrameworkReadyReceipt {
	return EnvironmentQualificationFrameworkReadyReceipt{
		RequestID: pgUUIDString(row.RequestID), Attempt: row.Attempt, GraphID: pgUUIDString(row.GraphID),
		CaptureInstanceID: pgUUIDString(row.CaptureInstanceID), InstanceID: pgUUIDString(row.InstanceID),
		Runtime: row.Runtime, WarmupMS: row.WarmupMs, RecordedAt: row.RecordedAt.Time,
	}
}

func (s *PgStore) EnvironmentQualificationFrameworkReadyReceipt(ctx context.Context, requestID string,
	attempt int64) (EnvironmentQualificationFrameworkReadyReceipt, error) {
	if !qualificationRecoveryUUIDValid(requestID) || attempt < 1 {
		return EnvironmentQualificationFrameworkReadyReceipt{}, ErrInvalidArgument
	}
	row, err := sqlc.New().EnvironmentQualificationFrameworkReadyReceipt(ctx, s.pool, sqlc.EnvironmentQualificationFrameworkReadyReceiptParams{
		RequestID: mustPgUUID(requestID), Attempt: attempt,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return EnvironmentQualificationFrameworkReadyReceipt{}, ErrNotFound
	}
	if err != nil {
		return EnvironmentQualificationFrameworkReadyReceipt{}, mapErr(err)
	}
	return qualificationFrameworkReadyReceiptFromSQL(row), nil
}

func (s *PgStore) RecordEnvironmentQualificationFrameworkReadyReceipt(ctx context.Context,
	frame EnvironmentQualificationExecution, runtimeLabel string, warmupMS int64) (EnvironmentQualificationFrameworkReadyReceipt, error) {
	if !qualificationRecoveryUUIDValid(frame.RequestID) || !qualificationRecoveryUUIDValid(frame.GraphID) ||
		!qualificationRecoveryUUIDValid(frame.CaptureInstanceID) || !qualificationRecoveryUUIDValid(frame.InstanceID) ||
		frame.CaptureInstanceID == frame.InstanceID || frame.Attempt < 1 || !qualificationFrameworkReadyRuntimeValid(runtimeLabel) ||
		warmupMS < 0 || warmupMS > qualificationFrameworkReadyWarmupMaxMS {
		return EnvironmentQualificationFrameworkReadyReceipt{}, ErrInvalidArgument
	}
	prior, err := s.EnvironmentQualificationFrameworkReadyReceipt(ctx, frame.RequestID, frame.Attempt)
	if err == nil {
		if !qualificationFrameworkReadyReceiptMatches(prior, frame, runtimeLabel, warmupMS) {
			return EnvironmentQualificationFrameworkReadyReceipt{}, ErrConflict
		}
		return prior, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return EnvironmentQualificationFrameworkReadyReceipt{}, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return EnvironmentQualificationFrameworkReadyReceipt{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	requestRow, err := s.qualificationCurrentTx(ctx, tx, frame.RequestID)
	if err != nil {
		return EnvironmentQualificationFrameworkReadyReceipt{}, err
	}
	current := qualificationRequestFromSQL(requestRow)
	if current.Attempt != frame.Attempt || current.GraphID != frame.GraphID || current.ReservedInstanceID != frame.CaptureInstanceID ||
		current.Phase != "claimed" || current.FrozenInputs.RuntimeBase != "" && current.FrozenInputs.RuntimeBase != runtimeLabel || current.LeaseUntil == nil ||
		!current.LeaseUntil.After(time.Now()) || current.LeaseToken == "" {
		return EnvironmentQualificationFrameworkReadyReceipt{}, ErrConflict
	}
	if _, err := q.SetEnvironmentWorkloadQualificationContext(ctx, tx, current.LeaseToken); err != nil {
		return EnvironmentQualificationFrameworkReadyReceipt{}, mapErr(err)
	}
	executionRow, err := q.LockEnvironmentQualificationExecution(ctx, tx, mustPgUUID(frame.InstanceID))
	if errors.Is(err, pgx.ErrNoRows) {
		return EnvironmentQualificationFrameworkReadyReceipt{}, ErrConflict
	}
	if err != nil {
		return EnvironmentQualificationFrameworkReadyReceipt{}, mapErr(err)
	}
	status, err := qualificationExecutionFromSQL(executionRow)
	if err != nil || status.Execution != frame || status.CaptureInstanceID != frame.CaptureInstanceID || !status.DispatchStarted || status.RetiredAt != nil {
		return EnvironmentQualificationFrameworkReadyReceipt{}, errors.Join(err, ErrConflict)
	}
	instanceRow, err := q.LockEnvironmentQualificationRuntimeInstance(ctx, tx, mustPgUUID(frame.InstanceID))
	if errors.Is(err, pgx.ErrNoRows) {
		return EnvironmentQualificationFrameworkReadyReceipt{}, ErrConflict
	}
	if err != nil {
		return EnvironmentQualificationFrameworkReadyReceipt{}, mapErr(err)
	}
	instance := qualificationInstanceFromSQL(instanceRow)
	if instance.State != string(StateRunning) || instance.WakeID != frame.WakeID || instance.AppID != frame.AppID ||
		instance.DeploymentID != frame.DeploymentID {
		return EnvironmentQualificationFrameworkReadyReceipt{}, ErrConflict
	}
	captureRow, err := q.EnvironmentQualificationSnapshotReceipt(ctx, tx, mustPgUUID(frame.CaptureInstanceID))
	if errors.Is(err, pgx.ErrNoRows) {
		return EnvironmentQualificationFrameworkReadyReceipt{}, ErrConflict
	}
	if err != nil {
		return EnvironmentQualificationFrameworkReadyReceipt{}, mapErr(err)
	}
	capture, err := qualificationSnapshotReceiptFromSQL(captureRow)
	if err != nil {
		return EnvironmentQualificationFrameworkReadyReceipt{}, err
	}
	runtimeRow, err := q.InstanceRuntimeConfigReceipt(ctx, tx, mustPgUUID(frame.InstanceID))
	if errors.Is(err, pgx.ErrNoRows) {
		return EnvironmentQualificationFrameworkReadyReceipt{}, ErrConflict
	}
	if err != nil {
		return EnvironmentQualificationFrameworkReadyReceipt{}, mapErr(err)
	}
	inputs, err := runtimeConfigInputsFromSQL(runtimeRow.Scope, runtimeRow.BoundaryAt, runtimeRow.Variables, runtimeRow.SecretVersions,
		runtimeRow.SecretRefs, runtimeRow.SidecarSecretVersions, runtimeRow.AllSecrets)
	if err != nil || pgUUIDString(runtimeRow.WakeID) != frame.WakeID {
		return EnvironmentQualificationFrameworkReadyReceipt{}, errors.Join(err, ErrConflict)
	}
	if !qualificationRuntimeValuesEqual(inputs, capture.Inputs) {
		return EnvironmentQualificationFrameworkReadyReceipt{}, errors.Join(err, ErrConflict)
	}
	fresh, err := readRuntimeConfigInputsFresh(ctx, tx, current.AppID, inputs)
	if err != nil || !fresh {
		return EnvironmentQualificationFrameworkReadyReceipt{}, errors.Join(err, ErrConflict)
	}
	stored, err := q.InsertEnvironmentQualificationFrameworkReadyReceipt(ctx, tx, sqlc.InsertEnvironmentQualificationFrameworkReadyReceiptParams{
		RequestID: mustPgUUID(frame.RequestID), Attempt: frame.Attempt, GraphID: mustPgUUID(frame.GraphID),
		CaptureInstanceID: mustPgUUID(frame.CaptureInstanceID), InstanceID: mustPgUUID(frame.InstanceID), Runtime: runtimeLabel,
		WarmupMs: warmupMS,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		stored, err = q.EnvironmentQualificationFrameworkReadyReceipt(ctx, tx, sqlc.EnvironmentQualificationFrameworkReadyReceiptParams{
			RequestID: mustPgUUID(frame.RequestID), Attempt: frame.Attempt,
		})
	}
	if err != nil {
		return EnvironmentQualificationFrameworkReadyReceipt{}, mapErr(err)
	}
	receipt := qualificationFrameworkReadyReceiptFromSQL(stored)
	if !qualificationFrameworkReadyReceiptMatches(receipt, frame, runtimeLabel, warmupMS) {
		return EnvironmentQualificationFrameworkReadyReceipt{}, ErrConflict
	}
	if err := tx.Commit(ctx); err != nil {
		return EnvironmentQualificationFrameworkReadyReceipt{}, err
	}
	return receipt, nil
}
