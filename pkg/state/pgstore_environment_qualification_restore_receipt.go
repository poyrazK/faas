package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ EnvironmentQualificationRestoreReceiptStore = (*PgStore)(nil)

func qualificationRestoreReceiptFromSQL(row sqlc.EnvironmentQualificationRestoreReceipt) (EnvironmentQualificationRestoreReceipt, error) {
	var receipt EnvironmentQualificationRestoreReceipt
	receipt.RequestID = pgUUIDString(row.RequestID)
	receipt.Attempt = row.Attempt
	receipt.CaptureInstanceID = pgUUIDString(row.CaptureInstanceID)
	receipt.InstanceID = pgUUIDString(row.InstanceID)
	if err := json.Unmarshal(row.RuntimeInputs, &receipt.Inputs); err != nil {
		return receipt, err
	}
	receipt.RecordedAt = row.RecordedAt.Time
	return receipt, nil
}

func (s *PgStore) EnvironmentQualificationRestoreReceipt(ctx context.Context, requestID string, attempt int64) (EnvironmentQualificationRestoreReceipt, error) {
	if !qualificationRecoveryUUIDValid(requestID) || attempt < 1 {
		return EnvironmentQualificationRestoreReceipt{}, ErrInvalidArgument
	}
	row, err := sqlc.New().EnvironmentQualificationRestoreReceipt(ctx, s.pool, sqlc.EnvironmentQualificationRestoreReceiptParams{
		RequestID: mustPgUUID(requestID), Attempt: attempt})
	if err != nil {
		return EnvironmentQualificationRestoreReceipt{}, mapErr(err)
	}
	return qualificationRestoreReceiptFromSQL(row)
}

func (s *PgStore) RecordEnvironmentQualificationRestoreReceipt(ctx context.Context, claimed EnvironmentWorkloadQualificationRequest,
	frame EnvironmentQualificationExecution, inputs RuntimeConfigInputs) (EnvironmentQualificationRestoreReceipt, error) {
	var zero EnvironmentQualificationRestoreReceipt
	if !qualificationRecoveryUUIDValid(claimed.ID) || !qualificationRecoveryUUIDValid(frame.InstanceID) || claimed.Attempt < 1 || frame.CaptureInstanceID != claimed.ReservedInstanceID {
		return zero, ErrInvalidArgument
	}
	inputs = normalizeQualificationRestoreInputs(inputs)
	q := sqlc.New()
	prior, err := q.EnvironmentQualificationRestoreReceipt(ctx, s.pool, sqlc.EnvironmentQualificationRestoreReceiptParams{
		RequestID: mustPgUUID(claimed.ID), Attempt: claimed.Attempt})
	if err == nil {
		receipt, err := qualificationRestoreReceiptFromSQL(prior)
		if err != nil {
			return zero, err
		}
		if !qualificationRestoreReceiptMatchesRequest(receipt, claimed, frame, inputs) {
			return zero, ErrConflict
		}
		return receipt, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return zero, mapErr(err)
	}
	if err := validateRuntimeConfigInputs(inputs); err != nil {
		return zero, err
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
	if !qualificationClaimIdentityMatches(current, claimed) || current.Attempt != frame.Attempt || frame.CaptureInstanceID != current.ReservedInstanceID {
		return zero, ErrConflict
	}
	if _, err := q.SetEnvironmentWorkloadQualificationContext(ctx, tx, claimed.LeaseToken); err != nil {
		return zero, mapErr(err)
	}
	originalRow, err := q.LockEnvironmentQualificationExecution(ctx, tx, mustPgUUID(current.ReservedInstanceID))
	if err != nil {
		return zero, mapErr(err)
	}
	original, err := qualificationExecutionFromSQL(originalRow)
	if err != nil {
		return zero, err
	}
	captureRow, err := q.EnvironmentQualificationSnapshotReceipt(ctx, tx, mustPgUUID(current.ReservedInstanceID))
	if err != nil {
		return zero, mapErr(err)
	}
	capture, err := qualificationSnapshotReceiptFromSQL(captureRow)
	if err != nil {
		return zero, err
	}
	targetRow, err := q.LockEnvironmentQualificationExecution(ctx, tx, mustPgUUID(frame.InstanceID))
	if err != nil {
		return zero, mapErr(err)
	}
	target, err := qualificationExecutionFromSQL(targetRow)
	if err != nil {
		return zero, err
	}
	if target.Execution != frame {
		return zero, ErrConflict
	}
	instanceRow, err := q.LockEnvironmentQualificationRuntimeInstance(ctx, tx, mustPgUUID(frame.InstanceID))
	if err != nil {
		return zero, mapErr(err)
	}
	instance := qualificationInstanceFromSQL(instanceRow)
	if instance.State != string(StateStopped) || instance.AppID != frame.AppID || instance.DeploymentID != frame.DeploymentID ||
		instance.NodeID != frame.NodeID || instance.WakeID != frame.WakeID {
		return zero, fmt.Errorf("state: restore runtime target is not the retired matching instance: %w", ErrConflict)
	}
	config, err := q.InstanceRuntimeConfigReceipt(ctx, tx, mustPgUUID(frame.InstanceID))
	if err != nil {
		return zero, mapErr(err)
	}
	recordedInputs, err := runtimeConfigInputsFromSQL(config.Scope, config.BoundaryAt, config.Variables, config.SecretVersions,
		config.SecretRefs, config.SidecarSecretVersions, config.AllSecrets)
	if err != nil || pgUUIDString(config.WakeID) != frame.WakeID || !runtimeConfigInputsPostgresEqual(recordedInputs, inputs) {
		return zero, fmt.Errorf("state: restore runtime receipt does not match its acknowledged inputs: %w", ErrConflict)
	}
	sourceGuestConfig, err := q.EnvironmentQualificationConfigReceipt(ctx, tx, mustPgUUID(current.ReservedInstanceID))
	if err != nil || !qualificationConfigReceiptMatchesAttempt(qualificationConfigReceiptFromSQL(sourceGuestConfig), current,
		current.ReservedInstanceID, "") {
		return zero, fmt.Errorf("state: source guest configuration acknowledgement is missing: %w", ErrConflict)
	}
	targetGuestConfig, err := q.EnvironmentQualificationConfigReceipt(ctx, tx, mustPgUUID(frame.InstanceID))
	if err != nil || !qualificationConfigReceiptMatchesFrame(qualificationConfigReceiptFromSQL(targetGuestConfig), current, frame) {
		return zero, fmt.Errorf("state: restore guest configuration acknowledgement is missing: %w", ErrConflict)
	}
	captureFresh, err := readRuntimeConfigInputsFresh(ctx, tx, current.AppID, capture.Inputs)
	if err != nil {
		return zero, err
	}
	fresh, err := readRuntimeConfigInputsFresh(ctx, tx, current.AppID, inputs)
	if err != nil {
		return zero, err
	}
	if !captureFresh {
		return zero, fmt.Errorf("state: captured runtime inputs are stale: %w", ErrConflict)
	}
	if !fresh {
		return zero, fmt.Errorf("state: restored runtime inputs are stale: %w", ErrConflict)
	}
	if !qualificationRestoreReceiptValid(current, original, capture, target, inputs) {
		return zero, fmt.Errorf("state: capture and restore evidence do not describe the same isolated workload: %w", ErrConflict)
	}
	rawInputs, err := json.Marshal(inputs)
	if err != nil {
		return zero, err
	}
	stored, err := q.RecordEnvironmentQualificationRestoreReceipt(ctx, tx, sqlc.RecordEnvironmentQualificationRestoreReceiptParams{
		RequestID: mustPgUUID(current.ID), Attempt: current.Attempt, CaptureInstanceID: mustPgUUID(current.ReservedInstanceID),
		InstanceID: mustPgUUID(frame.InstanceID), RuntimeInputs: rawInputs})
	if errors.Is(err, pgx.ErrNoRows) {
		stored, err = q.EnvironmentQualificationRestoreReceipt(ctx, tx, sqlc.EnvironmentQualificationRestoreReceiptParams{
			RequestID: mustPgUUID(current.ID), Attempt: current.Attempt})
		if errors.Is(err, pgx.ErrNoRows) {
			return zero, ErrConflict
		}
	}
	if err != nil {
		return zero, mapErr(err)
	}
	receipt, err := qualificationRestoreReceiptFromSQL(stored)
	if err != nil {
		return zero, err
	}
	if !qualificationRestoreReceiptMatchesRequest(receipt, claimed, frame, inputs) {
		return zero, ErrConflict
	}
	if err := tx.Commit(ctx); err != nil {
		return zero, err
	}
	return receipt, nil
}
