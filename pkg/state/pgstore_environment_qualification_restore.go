package state

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ EnvironmentQualificationRestoreStore = (*PgStore)(nil)

func (s *PgStore) qualificationRestoreCurrentTx(ctx context.Context, tx pgx.Tx, claimed EnvironmentWorkloadQualificationRequest) (sqlc.EnvironmentWorkloadQualificationRequest, EnvironmentQualificationSnapshotReceipt, error) {
	var capture EnvironmentQualificationSnapshotReceipt
	row, err := s.qualificationCurrentTx(ctx, tx, claimed.ID)
	if err != nil {
		return row, capture, err
	}
	current := qualificationRequestFromSQL(row)
	if !qualificationLeaseMatches(current, claimed, time.Now()) || current.ReservedInstanceID == "" {
		return row, capture, ErrConflict
	}
	q := sqlc.New()
	if _, err := q.SetEnvironmentWorkloadQualificationContext(ctx, tx, claimed.LeaseToken); err != nil {
		return row, capture, mapErr(err)
	}
	original, err := q.LockEnvironmentQualificationExecution(ctx, tx, row.ReservedInstanceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return row, capture, ErrConflict
	}
	if err != nil {
		return row, capture, mapErr(err)
	}
	status, err := qualificationExecutionFromSQL(original)
	if err != nil {
		return row, capture, err
	}
	receipt, err := q.EnvironmentQualificationSnapshotReceipt(ctx, tx, row.ReservedInstanceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return row, capture, ErrConflict
	}
	if err != nil {
		return row, capture, mapErr(err)
	}
	capture, err = qualificationSnapshotReceiptFromSQL(receipt)
	if err != nil {
		return row, capture, err
	}
	if !qualificationRestoreCaptureMatches(current, status, capture) {
		return row, capture, ErrConflict
	}
	fresh, err := q.EnvironmentQualificationRestoreCurrent(ctx, tx, sqlc.EnvironmentQualificationRestoreCurrentParams{RequestID: row.ID, CaptureInstanceID: row.ReservedInstanceID})
	if err != nil {
		return row, capture, mapErr(err)
	}
	if !fresh {
		return row, capture, ErrConflict
	}
	return row, capture, nil
}

func (s *PgStore) CreateEnvironmentQualificationRestore(ctx context.Context, claimed EnvironmentWorkloadQualificationRequest, placement EnvironmentWorkloadQualificationPlacement) (EnvironmentQualificationRestoreAdmission, error) {
	if !qualificationPlacementValid(placement) {
		return EnvironmentQualificationRestoreAdmission{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return EnvironmentQualificationRestoreAdmission{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	row, capture, err := s.qualificationRestoreCurrentTx(ctx, tx, claimed)
	if err != nil {
		return EnvironmentQualificationRestoreAdmission{}, err
	}
	current := qualificationRequestFromSQL(row)
	if placement.WakeID == capture.Execution.WakeID {
		return EnvironmentQualificationRestoreAdmission{}, ErrConflict
	}
	q := sqlc.New()
	// Match ordinary admission's node -> account reservation order after the
	// reviewed source/app authority locks, including an idempotent retry.
	if err := q.LockEnvironmentQualificationNode(ctx, tx, sqlc.LockEnvironmentQualificationNodeParams{
		LockClass: nodeReservationLockClass, NodeID: placement.NodeID}); err != nil {
		return EnvironmentQualificationRestoreAdmission{}, mapErr(err)
	}
	inputs, err := q.EnvironmentQualificationAdmissionInputs(ctx, tx, sqlc.EnvironmentQualificationAdmissionInputsParams{
		AppID: mustPgUUID(current.AppID), NodeID: mustPgUUID(placement.NodeID)})
	if err != nil || int(inputs.RamMb) != placement.RAMMB {
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return EnvironmentQualificationRestoreAdmission{}, mapErr(err)
		}
		return EnvironmentQualificationRestoreAdmission{}, ErrConflict
	}
	// Keep account status stable through both initial admission and a retry.
	// An UPDATE lock avoids a later worker quota lock upgrade across sources.
	mayDeploy, err := q.LockEnvironmentQualificationAccount(ctx, tx, row.AppID)
	if err != nil {
		return EnvironmentQualificationRestoreAdmission{}, mapErr(err)
	}
	if !mayDeploy.Valid || !mayDeploy.Bool {
		return EnvironmentQualificationRestoreAdmission{}, ErrConflict
	}
	reservation, err := q.EnvironmentQualificationRestoreReservation(ctx, tx, sqlc.EnvironmentQualificationRestoreReservationParams{RequestID: row.ID, Attempt: row.Attempt})
	if err == nil {
		prior, err := q.EnvironmentQualificationInstance(ctx, tx, reservation.InstanceID)
		if errors.Is(err, pgx.ErrNoRows) {
			return EnvironmentQualificationRestoreAdmission{}, ErrConflict
		}
		if err != nil {
			return EnvironmentQualificationRestoreAdmission{}, mapErr(err)
		}
		ins := qualificationInstanceFromSQL(prior)
		if !qualificationRestoreAdmissionMatches(ins, current, placement) || !qualificationLeaseMatches(current, claimed, time.Now()) {
			return EnvironmentQualificationRestoreAdmission{}, ErrConflict
		}
		frame, err := q.EnvironmentQualificationExecution(ctx, tx, prior.ID)
		if err != nil {
			return EnvironmentQualificationRestoreAdmission{}, mapErr(err)
		}
		status, err := qualificationExecutionFromSQL(frame)
		if err != nil || status.CaptureInstanceID != capture.Execution.InstanceID || status.RetiredAt != nil || !qualificationExecutionMatches(status.Execution, qualificationRestoreExecution(current, ins, status.Execution.CleanupToken, capture.Execution.InstanceID)) {
			return EnvironmentQualificationRestoreAdmission{}, ErrConflict
		}
		return EnvironmentQualificationRestoreAdmission{Instance: ins, Execution: status.Execution, Capture: capture}, mapErr(tx.Commit(ctx))
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return EnvironmentQualificationRestoreAdmission{}, mapErr(err)
	}
	used, err := q.EnvironmentQualificationNodeUsedMB(ctx, tx, sqlc.EnvironmentQualificationNodeUsedMBParams{
		NodeID: mustPgUUID(placement.NodeID), OverheadMb: api.PerVMOverheadMB})
	if err != nil {
		return EnvironmentQualificationRestoreAdmission{}, mapErr(err)
	}
	if used+int64(placement.RAMMB+api.PerVMOverheadMB) > int64(inputs.AdmissionCeilingMb) {
		return EnvironmentQualificationRestoreAdmission{}, ErrNodeCapacity
	}
	if current.ExecutionMode == api.ExecutionModeWorker {
		if err := reserveAccountWorker(ctx, tx, current.AppID, current.DeploymentID); err != nil {
			return EnvironmentQualificationRestoreAdmission{}, err
		}
	}
	if _, err := q.SetEnvironmentWorkloadQualificationContext(ctx, tx, claimed.LeaseToken); err != nil {
		return EnvironmentQualificationRestoreAdmission{}, mapErr(err)
	}
	target := uuid.NewString()
	if err := q.ReserveEnvironmentQualificationRestore(ctx, tx, sqlc.ReserveEnvironmentQualificationRestoreParams{InstanceID: mustPgUUID(target),
		CaptureInstanceID: mustPgUUID(capture.Execution.InstanceID), RequestID: row.ID, Attempt: row.Attempt}); err != nil {
		return EnvironmentQualificationRestoreAdmission{}, mapErr(err)
	}
	ins, err := q.CreateEnvironmentQualificationInstance(ctx, tx, sqlc.CreateEnvironmentQualificationInstanceParams{
		ID: mustPgUUID(target), AppID: row.AppID, DeploymentID: row.DeploymentID,
		NodeID: mustPgUUID(placement.NodeID), WakeID: mustPgUUID(placement.WakeID), RamMb: int32(placement.RAMMB), Mode: qualificationInstanceMode(current)})
	if err != nil {
		return EnvironmentQualificationRestoreAdmission{}, mapErr(err)
	}
	if !qualificationLeaseMatches(current, claimed, time.Now()) {
		return EnvironmentQualificationRestoreAdmission{}, ErrConflict
	}
	frame, err := q.EnvironmentQualificationExecution(ctx, tx, ins.ID)
	if err != nil {
		return EnvironmentQualificationRestoreAdmission{}, mapErr(err)
	}
	status, err := qualificationExecutionFromSQL(frame)
	if err != nil {
		return EnvironmentQualificationRestoreAdmission{}, err
	}
	return EnvironmentQualificationRestoreAdmission{Instance: qualificationInstanceFromSQL(ins), Execution: status.Execution, Capture: capture, Created: true}, mapErr(tx.Commit(ctx))
}

func (s *PgStore) MarkEnvironmentQualificationRestoreDispatched(ctx context.Context, claimed EnvironmentWorkloadQualificationRequest, frame EnvironmentQualificationExecution) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	row, capture, err := s.qualificationRestoreCurrentTx(ctx, tx, claimed)
	if err != nil {
		return err
	}
	q := sqlc.New()
	stored, err := q.LockEnvironmentQualificationExecution(ctx, tx, mustPgUUID(frame.InstanceID))
	if err != nil {
		return mapErr(err)
	}
	status, err := qualificationExecutionFromSQL(stored)
	if err != nil {
		return err
	}
	if status.CaptureInstanceID != capture.Execution.InstanceID || status.Execution != frame || status.DispatchStarted || status.RetiredAt != nil || frame.RequestID != pgUUIDString(row.ID) || frame.Attempt != row.Attempt {
		return ErrConflict
	}
	count, err := q.MarkEnvironmentQualificationDispatched(ctx, tx, stored.InstanceID)
	if err != nil {
		return mapErr(err)
	}
	if count != 1 || !qualificationLeaseMatches(qualificationRequestFromSQL(row), claimed, time.Now()) {
		return ErrConflict
	}
	return mapErr(tx.Commit(ctx))
}
