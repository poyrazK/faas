// adr: 1196
package state

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// OperationDeadlineStore settles workflow Operations past their deadline.
// HTTP executions need no sweep: their invocation carries the deadline and
// the invocation deadline reaper settles them.
type OperationDeadlineStore interface {
	SettleOperationDeadlines(ctx context.Context, now time.Time, limit int) (int, error)
}

// operationDeadlineDue reports whether a workflow Operation should be
// stopped by its deadline now.
func operationDeadlineDue(op Operation, now time.Time) bool {
	active := op.State == api.OperationAccepted || op.State == api.OperationRunning || op.State == api.OperationWaiting
	return active && op.WorkflowRunID != "" && op.LimitExceeded == "" &&
		operationLimitExceeded(op, now) == api.OperationFailureDeadlineExceeded
}

const operationDeadlineReason = "operation deadline exceeded"

func (s *PgStore) SettleOperationDeadlines(ctx context.Context, now time.Time, limit int) (int, error) {
	if limit < 1 {
		return 0, ErrInvalidArgument
	}
	runs, err := sqlc.New().ListDueCustomerOperationDeadlines(ctx, s.pool, sqlc.ListDueCustomerOperationDeadlinesParams{Now: pgtype.Timestamptz{Time: now, Valid: true}, PageLimit: int32(limit)})
	if err != nil {
		return 0, fmt.Errorf("state: list operation deadlines: %w", err)
	}
	settled := 0
	for _, run := range runs {
		ok, err := s.settleOperationWorkflowDeadline(ctx, run, now)
		if err != nil {
			return settled, err
		}
		if ok {
			settled++
		}
	}
	return settled, nil
}

// settleOperationWorkflowDeadline takes the cancellation lock order (run,
// then operation) and rechecks the deadline under those locks.
func (s *PgStore) settleOperationWorkflowDeadline(ctx context.Context, runID string, now time.Time) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	run := mustPgUUID(runID)
	if _, err := q.LockWorkflowRecovery(ctx, tx, run); err != nil {
		return false, mapErr(err)
	}
	raw, err := q.LockCustomerOperationForWorkflow(ctx, tx, run)
	if err != nil {
		return false, mapErr(err)
	}
	op, err := operationPGRecord(raw)
	if err != nil {
		return false, err
	}
	if !operationDeadlineDue(op, now) {
		return false, tx.Commit(ctx)
	}
	op.LimitExceeded = api.OperationFailureDeadlineExceeded
	record, err := operationRecordJSON(op)
	if err != nil {
		return false, err
	}
	id, _ := operationUUID(op.ID)
	if err := q.UpdateCustomerOperation(ctx, tx, sqlc.UpdateCustomerOperationParams{ID: id, WorkflowRunID: run, JobRunID: mustPgUUID(op.JobRunID), State: string(op.State), Record: record, ExpiresAt: pgtype.Timestamptz{Time: op.ExpiresAt, Valid: true}}); err != nil {
		return false, fmt.Errorf("state: mark operation deadline: %w", err)
	}
	if err := stopOperationWorkflowTx(ctx, tx, runID, operationDeadlineReason); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

func (m *MemStore) SettleOperationDeadlines(_ context.Context, now time.Time, limit int) (int, error) {
	if limit < 1 {
		return 0, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	data := m.operationMemoryLocked()
	settled := 0
	for id, op := range data.operations {
		if settled >= limit {
			break
		}
		if !operationDeadlineDue(op, now) {
			continue
		}
		op = cloneOperation(op)
		op.LimitExceeded = api.OperationFailureDeadlineExceeded
		data.operations[id] = op
		if err := m.stopOperationWorkflowLocked(m.workflowRuns[op.WorkflowRunID], operationDeadlineReason, now); err != nil {
			return settled, err
		}
		settled++
	}
	return settled, nil
}

var (
	_ OperationDeadlineStore = (*PgStore)(nil)
	_ OperationDeadlineStore = (*MemStore)(nil)
)
