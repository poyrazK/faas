package state

import (
	"context"
	"encoding/json"
	"time"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ EnvironmentQualificationExecutionStore = (*PgStore)(nil)

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
