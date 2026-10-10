package state

import (
	"context"
	"encoding/json"
	"time"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ EnvironmentQualificationSnapshotStore = (*PgStore)(nil)

func qualificationSnapshotReceiptFromSQL(row sqlc.EnvironmentQualificationSnapshotReceiptRow) (EnvironmentQualificationSnapshotReceipt, error) {
	var receipt EnvironmentQualificationSnapshotReceipt
	if err := json.Unmarshal(row.Frame, &receipt.Execution); err != nil {
		return receipt, err
	}
	receipt.Execution.CleanupToken = pgUUIDString(row.CleanupToken)
	if err := json.Unmarshal(row.Snapshot, &receipt.Snapshot); err != nil {
		return receipt, err
	}
	if err := json.Unmarshal(row.Inputs, &receipt.Inputs); err != nil {
		return receipt, err
	}
	receipt.RecordedAt = row.RecordedAt.Time
	return receipt, nil
}

func (s *PgStore) EnvironmentQualificationSnapshotReceipt(ctx context.Context, instanceID string) (EnvironmentQualificationSnapshotReceipt, error) {
	row, err := sqlc.New().EnvironmentQualificationSnapshotReceipt(ctx, s.pool, mustPgUUID(instanceID))
	if err != nil {
		return EnvironmentQualificationSnapshotReceipt{}, mapErr(err)
	}
	return qualificationSnapshotReceiptFromSQL(row)
}

func (s *PgStore) RecordEnvironmentQualificationSnapshot(ctx context.Context, claimed EnvironmentWorkloadQualificationRequest, frame EnvironmentQualificationExecution, proof EnvironmentQualificationSnapshot) (EnvironmentQualificationSnapshotReceipt, error) {
	var zero EnvironmentQualificationSnapshotReceipt
	if err := ValidateEnvironmentQualificationSnapshot(frame, proof); err != nil {
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
	if !qualificationLeaseMatches(qualificationRequestFromSQL(row), claimed, time.Now()) || claimed.ReservedInstanceID != frame.InstanceID {
		return zero, ErrConflict
	}
	q := sqlc.New()
	stored, err := q.LockEnvironmentQualificationExecution(ctx, tx, row.ReservedInstanceID)
	if err != nil {
		return zero, mapErr(err)
	}
	execution, err := qualificationExecutionFromSQL(stored)
	if err != nil {
		return zero, err
	}
	if execution.Execution != frame || !execution.DispatchStarted || execution.RetiredAt != nil {
		return zero, ErrConflict
	}
	instance, err := q.LockEnvironmentQualificationRuntimeInstance(ctx, tx, row.ReservedInstanceID)
	if err != nil {
		return zero, mapErr(err)
	}
	ins := qualificationInstanceFromSQL(instance)
	if ins.State != string(StateRunning) || ins.WakeID != frame.WakeID || ins.NodeID != frame.NodeID {
		return zero, ErrConflict
	}
	config, err := q.InstanceRuntimeConfigReceipt(ctx, tx, row.ReservedInstanceID)
	if err != nil {
		return zero, mapErr(err)
	}
	inputs, err := runtimeConfigInputsFromSQL(config.Scope, config.BoundaryAt, config.Variables, config.SecretVersions, config.SecretRefs, config.SidecarSecretVersions, config.AllSecrets)
	if err != nil || pgUUIDString(config.WakeID) != frame.WakeID {
		return zero, ErrConflict
	}
	fresh, err := readRuntimeConfigInputsFresh(ctx, tx, frame.AppID, inputs)
	if err != nil {
		return zero, err
	}
	if !fresh {
		return zero, ErrConflict
	}
	if _, err := q.SetEnvironmentWorkloadQualificationContext(ctx, tx, claimed.LeaseToken); err != nil {
		return zero, mapErr(err)
	}
	rawProof, _ := json.Marshal(proof)
	rawInputs, _ := json.Marshal(inputs)
	if err := q.RecordEnvironmentQualificationSnapshot(ctx, tx, sqlc.RecordEnvironmentQualificationSnapshotParams{InstanceID: row.ReservedInstanceID, Snapshot: rawProof, Inputs: rawInputs}); err != nil {
		return zero, mapErr(err)
	}
	published, err := q.EnvironmentQualificationSnapshotReceipt(ctx, tx, row.ReservedInstanceID)
	if err != nil {
		return zero, mapErr(err)
	}
	receipt, err := qualificationSnapshotReceiptFromSQL(published)
	if err != nil {
		return zero, err
	}
	if receipt.Snapshot != proof || !qualificationCaptureInputsEqual(receipt.Inputs, inputs) || receipt.Execution != frame || !qualificationLeaseMatches(qualificationRequestFromSQL(row), claimed, time.Now()) {
		return zero, ErrConflict
	}
	return receipt, mapErr(tx.Commit(ctx))
}
