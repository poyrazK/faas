// adr: 597
package state

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type OperationWorkflowControlStore interface {
	WorkflowOperationExecutionControl(context.Context, string, OperationWorkflowAuthority) (api.OperationWorkflowControlResponse, error)
}

// WorkflowOperationDeadlineStore gives the scheduler the same fixed attempt
// deadline observed by the guest. It cannot extend or acquire a native lease.
type WorkflowOperationDeadlineStore interface {
	WorkflowOperationAttemptDeadline(context.Context, string, string, int) (time.Time, error)
}

type workflowOperationWindow struct{ deadline, lease, observed time.Time }

func operationWorkflowWindow(snapshot json.RawMessage, step string, started, lease, now time.Time) (workflowOperationWindow, error) {
	var spec api.WorkflowSpec
	if err := json.Unmarshal(snapshot, &spec); err != nil {
		return workflowOperationWindow{}, err
	}
	for _, action := range spec.Steps {
		if action.Name != step {
			continue
		}
		timeout := action.Timeout
		if timeout <= 0 {
			timeout = api.WorkflowStepDefaultTimeout
		}
		deadline := started.Add(timeout)
		if started.IsZero() || !deadline.After(now) || !lease.After(now) {
			return workflowOperationWindow{}, ErrOperationStaleAttempt
		}
		if lease.After(deadline) {
			lease = deadline
		}
		return workflowOperationWindow{deadline, lease, now}, nil
	}
	return workflowOperationWindow{}, ErrOperationStaleAttempt
}

func workflowOperationControl(op Operation, a OperationWorkflowAuthority, window workflowOperationWindow) api.OperationWorkflowControlResponse {
	return api.OperationWorkflowControlResponse{OperationID: op.ID, WorkflowRunID: a.RunID, WorkflowStep: a.StepName,
		Generation: a.Generation, Attempt: a.Attempt, CancellationRequested: op.CancellationRequested,
		DeadlineAt: window.deadline, LeaseExpiresAt: window.lease, ObservedAt: window.observed,
		PollAfterMS: max(api.OperationControlPollMinIntervalMS, min(api.OperationControlPollIntervalMS, int(window.lease.Sub(window.observed).Milliseconds()/3)))}
}

func (m *MemStore) workflowOperationWindowLocked(run WorkflowRun, step string, attempt int) (workflowOperationWindow, error) {
	record, exists := m.workflowStepAttempts[workflowStepAttemptKey{run.ID, step, attempt}]
	if !exists || record.Status != WorkflowAttemptStatusRunning {
		return workflowOperationWindow{}, ErrOperationStaleAttempt
	}
	return operationWorkflowWindow(run.DefinitionSnapshot, step, record.StartedAt, m.workflowRunLeases[run.ID], time.Now().UTC())
}

func (m *MemStore) WorkflowOperationExecutionControl(_ context.Context, id string, a OperationWorkflowAuthority) (api.OperationWorkflowControlResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	op, _, window, err := m.workflowOperationAuthorityLocked(id, a, false)
	if err != nil {
		return api.OperationWorkflowControlResponse{}, err
	}
	return workflowOperationControl(op, a, window), nil
}

func (m *MemStore) WorkflowOperationAttemptDeadline(ctx context.Context, runID, step string, attempt int) (time.Time, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	run := m.workflowRuns[runID]
	summary := m.workflowSteps[runID][step]
	if run.Status != WorkflowRunStatusRunning || summary.Status != WorkflowStepStatusRunning || summary.Attempt != attempt || !WorkflowRunGenerationMatches(ctx, runID, run.ResumeCount) {
		return time.Time{}, ErrOperationStaleAttempt
	}
	window, err := m.workflowOperationWindowLocked(run, step, attempt)
	return window.deadline, err
}

func workflowOperationWindowTx(ctx context.Context, db sqlc.DBTX, runID, step string, attempt int) (workflowOperationWindow, error) {
	if attempt < 1 || attempt > math.MaxInt32 {
		return workflowOperationWindow{}, ErrInvalidArgument
	}
	id, err := operationUUID(runID)
	if err != nil {
		return workflowOperationWindow{}, err
	}
	row, err := sqlc.New().CustomerOperationWorkflowAttemptWindow(ctx, db, sqlc.CustomerOperationWorkflowAttemptWindowParams{RunID: id, StepName: step, Attempt: int32(attempt)})
	if errors.Is(err, pgx.ErrNoRows) || err == nil && !WorkflowRunGenerationMatches(ctx, runID, int(row.ResumeCount)) {
		return workflowOperationWindow{}, ErrOperationStaleAttempt
	}
	if err != nil {
		return workflowOperationWindow{}, err
	}
	return operationWorkflowWindow(row.DefinitionSnapshot, step, row.StartedAt.Time, row.LeaseUntil.Time, row.ObservedAt.Time)
}

func checkWorkflowOperationSuccessDeadlineTx(ctx context.Context, db sqlc.DBTX, runID, step string, attempt int) error {
	_, err := sqlc.New().GetCustomerOperationForWorkflow(ctx, db, mustPgUUID(runID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = workflowOperationWindowTx(ctx, db, runID, step, attempt)
	return err
}

func (s *PgStore) WorkflowOperationExecutionControl(ctx context.Context, id string, a OperationWorkflowAuthority) (api.OperationWorkflowControlResponse, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.OperationWorkflowControlResponse{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// Match native recovery/artifact lock order: run, then operation. Every
	// proof and budget read uses this transaction; nothing is written or renewed.
	op, _, window, err := workflowOperationAuthorityTx(ctx, tx, id, a, false)
	if err != nil {
		return api.OperationWorkflowControlResponse{}, err
	}
	return workflowOperationControl(op, a, window), nil
}

func (s *PgStore) WorkflowOperationAttemptDeadline(ctx context.Context, runID, step string, attempt int) (time.Time, error) {
	window, err := workflowOperationWindowTx(ctx, s.pool, runID, step, attempt)
	return window.deadline, err
}
