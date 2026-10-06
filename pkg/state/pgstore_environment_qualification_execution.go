package state

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ EnvironmentQualificationExecutionStore = (*PgStore)(nil)
var _ EnvironmentQualificationRecoveryStore = (*PgStore)(nil)

func (s *PgStore) ListEnvironmentQualificationExecutionsForRecovery(ctx context.Context, nodeID, afterInstanceID string, limit int) ([]EnvironmentQualificationExecutionStatus, error) {
	if !qualificationRecoveryPageValid(nodeID, afterInstanceID, limit) {
		return nil, ErrInvalidArgument
	}
	rows, err := sqlc.New().ListEnvironmentQualificationExecutionsForRecovery(ctx, s.pool, sqlc.ListEnvironmentQualificationExecutionsForRecoveryParams{
		NodeID: pgUUIDString(mustPgUUID(nodeID)), AfterInstanceID: afterInstanceID, PageLimit: int32(limit)})
	if err != nil {
		return nil, mapErr(err)
	}
	statuses := make([]EnvironmentQualificationExecutionStatus, 0, len(rows))
	for _, row := range rows {
		status, err := qualificationExecutionFromSQL(row)
		if err != nil {
			return nil, err
		}
		statuses = append(statuses, status)
	}
	return statuses, nil
}

func (s *PgStore) EnvironmentQualificationExecutionForRecovery(ctx context.Context, nodeID, instanceID string) (EnvironmentQualificationExecutionStatus, error) {
	if !qualificationRecoveryUUIDValid(nodeID) || !qualificationRecoveryUUIDValid(instanceID) {
		return EnvironmentQualificationExecutionStatus{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return EnvironmentQualificationExecutionStatus{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	row, err := q.EnvironmentQualificationExecution(ctx, tx, mustPgUUID(instanceID))
	if err != nil {
		return EnvironmentQualificationExecutionStatus{}, mapErr(err)
	}
	// Request before frame matches dispatch/renewal lock order. A purge may
	// remove the request, but cannot remove or replace its immutable frame.
	if _, err := q.EnvironmentWorkloadQualificationForUpdate(ctx, tx, row.RequestID); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return EnvironmentQualificationExecutionStatus{}, mapErr(err)
	}
	row, err = q.LockEnvironmentQualificationExecution(ctx, tx, row.InstanceID)
	if err != nil {
		return EnvironmentQualificationExecutionStatus{}, mapErr(err)
	}
	status, err := qualificationExecutionFromSQL(row)
	if err != nil {
		return status, err
	}
	if status.Execution.NodeID != pgUUIDString(mustPgUUID(nodeID)) {
		return EnvironmentQualificationExecutionStatus{}, ErrNotFound
	}
	recoverable, err := q.EnvironmentQualificationExecutionRecoverable(ctx, tx, row.InstanceID)
	if err != nil {
		return EnvironmentQualificationExecutionStatus{}, mapErr(err)
	}
	if !recoverable.Valid || !recoverable.Bool {
		return EnvironmentQualificationExecutionStatus{}, ErrConflict
	}
	return status, mapErr(tx.Commit(ctx))
}

func qualificationExecutionFromSQL(row sqlc.EnvironmentQualificationExecution) (EnvironmentQualificationExecutionStatus, error) {
	status := EnvironmentQualificationExecutionStatus{DispatchStarted: row.DispatchStarted}
	if err := json.Unmarshal(row.Frame, &status.Execution); err != nil {
		return status, err
	}
	status.Execution.CleanupToken = pgUUIDString(row.CleanupToken)
	if row.RetiredAt.Valid {
		status.RetiredAt = &row.RetiredAt.Time
		if err := json.Unmarshal(row.Retirement, &status.Retirement); err != nil {
			return status, err
		}
	}
	return status, nil
}

func (s *PgStore) EnvironmentQualificationExecution(ctx context.Context, instanceID string) (EnvironmentQualificationExecutionStatus, error) {
	row, err := sqlc.New().EnvironmentQualificationExecution(ctx, s.pool, mustPgUUID(instanceID))
	if err != nil {
		return EnvironmentQualificationExecutionStatus{}, mapErr(err)
	}
	return qualificationExecutionFromSQL(row)
}

func (s *PgStore) MarkEnvironmentQualificationDispatched(ctx context.Context, claimed EnvironmentWorkloadQualificationRequest, execution EnvironmentQualificationExecution) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	row, err := s.qualificationCurrentTx(ctx, tx, claimed.ID)
	if err != nil {
		return err
	}
	current := qualificationRequestFromSQL(row)
	if !qualificationLeaseMatches(current, claimed, time.Now()) || current.ReservedInstanceID != execution.InstanceID {
		return ErrConflict
	}
	q := sqlc.New()
	stored, err := q.LockEnvironmentQualificationExecution(ctx, tx, row.ReservedInstanceID)
	if err != nil {
		return mapErr(err)
	}
	status, err := qualificationExecutionFromSQL(stored)
	if err != nil {
		return err
	}
	if !qualificationExecutionMatches(status.Execution, execution) || status.DispatchStarted || status.RetiredAt != nil {
		return ErrConflict
	}
	if _, err := q.SetEnvironmentWorkloadQualificationContext(ctx, tx, claimed.LeaseToken); err != nil {
		return mapErr(err)
	}
	count, err := q.MarkEnvironmentQualificationDispatched(ctx, tx, row.ReservedInstanceID)
	if err != nil {
		return mapErr(err)
	}
	if count != 1 {
		return ErrConflict
	}
	return mapErr(tx.Commit(ctx))
}

func (s *PgStore) RetireEnvironmentQualificationExecution(ctx context.Context, execution EnvironmentQualificationExecution, proof EnvironmentQualificationRetirement) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	row, err := q.LockEnvironmentQualificationExecution(ctx, tx, mustPgUUID(execution.InstanceID))
	if err != nil {
		return mapErr(err)
	}
	status, err := qualificationExecutionFromSQL(row)
	if err != nil {
		return err
	}
	if !qualificationExecutionMatches(status.Execution, execution) || !qualificationRetirementValid(proof, status.DispatchStarted) {
		return ErrConflict
	}
	if status.RetiredAt != nil {
		if !qualificationRetirementEqual(status.Retirement, proof) {
			return ErrConflict
		}
		return mapErr(tx.Commit(ctx))
	}
	instance, err := q.LockEnvironmentQualificationRuntimeInstance(ctx, tx, row.InstanceID)
	if err != nil {
		return mapErr(err)
	}
	ins := qualificationInstanceFromSQL(instance)
	_, valid := qualificationRetiredState(State(ins.State))
	if ins.AppID != execution.AppID || ins.DeploymentID != execution.DeploymentID || ins.NodeID != execution.NodeID || ins.WakeID != execution.WakeID || !valid {
		return ErrConflict
	}
	if _, err := q.SetEnvironmentQualificationCleanupContext(ctx, tx, execution.CleanupToken); err != nil {
		return mapErr(err)
	}
	data, _ := json.Marshal(proof)
	count, err := q.RetireEnvironmentQualificationExecution(ctx, tx, sqlc.RetireEnvironmentQualificationExecutionParams{InstanceID: row.InstanceID, Retirement: data})
	if err != nil {
		return mapErr(err)
	}
	if count != 1 {
		return ErrConflict
	}
	if _, err := q.StopEnvironmentQualificationInstance(ctx, tx, row.InstanceID); err != nil {
		return mapErr(err)
	}
	return mapErr(tx.Commit(ctx))
}
