package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/api"
)

var _ WorkflowCodeStore = (*PgStore)(nil)

// lockWorkflowCodeRunTx locks a non-terminal run and checks its generation.
func lockWorkflowCodeRunTx(ctx context.Context, tx pgx.Tx, runID string) (string, time.Time, error) {
	var status string
	var scheduledFor time.Time
	if err := tx.QueryRow(ctx, `SELECT status, scheduled_for FROM workflow_runs WHERE id = $1 FOR UPDATE`, runID).Scan(&status, &scheduledFor); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", time.Time{}, ErrWorkflowRunNotFound
		}
		return "", time.Time{}, fmt.Errorf("pgstore: lock code workflow run: %w", err)
	}
	if err := checkWorkflowGenerationTx(ctx, tx, runID); err != nil {
		return "", time.Time{}, err
	}
	if status == WorkflowRunStatusSucceeded || status == WorkflowRunStatusFailed || status == WorkflowRunStatusDead {
		return "", time.Time{}, fmt.Errorf("%w: workflow run is terminal", ErrConflict)
	}
	return status, scheduledFor, nil
}

func (s *PgStore) BeginWorkflowCodeInvocation(ctx context.Context, runID string, driverInput json.RawMessage) (int, error) {
	if err := validateWorkflowJSON(driverInput, true); err != nil {
		return 0, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("pgstore: begin code workflow invocation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck // no-op after Commit
	if _, _, err := lockWorkflowCodeRunTx(ctx, tx, runID); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO workflow_steps (run_id, step_name, status, attempt, input)
		VALUES ($1, $2, 'pending', 0, $3)
		ON CONFLICT (run_id, step_name) DO NOTHING
	`, runID, api.WorkflowCodeDriverStep, driverInput); err != nil {
		return 0, fmt.Errorf("pgstore: create code workflow driver: %w", err)
	}
	var status string
	var attempt int
	if err := tx.QueryRow(ctx, `SELECT status, attempt FROM workflow_steps WHERE run_id = $1 AND step_name = $2 FOR UPDATE`, runID, api.WorkflowCodeDriverStep).Scan(&status, &attempt); err != nil {
		return 0, fmt.Errorf("pgstore: lock code workflow driver: %w", err)
	}
	if workflowCodeTerminalStatus(status) {
		return 0, fmt.Errorf("%w: code workflow driver is %s", ErrConflict, status)
	}
	attempt++
	if _, err := tx.Exec(ctx, `
		UPDATE workflow_steps
		SET status = 'running', attempt = $3, input = $4, error = NULL,
		    next_retry_at = NULL, finished_at = NULL, started_at = COALESCE(started_at, now())
		WHERE run_id = $1 AND step_name = $2
	`, runID, api.WorkflowCodeDriverStep, attempt, driverInput); err != nil {
		return 0, fmt.Errorf("pgstore: start code workflow driver: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO workflow_step_attempts (run_id, step_name, attempt, status)
		VALUES ($1, $2, $3, 'running')
		ON CONFLICT (run_id, step_name, attempt) DO UPDATE
		SET status = 'running', http_status = NULL, started_at = now(), finished_at = NULL,
		    next_attempt_at = NULL, error = NULL
	`, runID, api.WorkflowCodeDriverStep, attempt); err != nil {
		return 0, fmt.Errorf("pgstore: start code workflow driver attempt: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE workflow_runs
		SET status = 'running', current_step = $2, started_at = COALESCE(started_at, now()), updated_at = now()
		WHERE id = $1
	`, runID, api.WorkflowCodeDriverStep); err != nil {
		return 0, fmt.Errorf("pgstore: mark code workflow running: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("pgstore: commit code workflow invocation start: %w", err)
	}
	return attempt, nil
}

func (s *PgStore) FinishWorkflowCodeInvocation(ctx context.Context, runID string, finish WorkflowCodeInvocation) error {
	if err := validateWorkflowCodeInvocation(finish); err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("pgstore: begin code workflow invocation finish: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck // no-op after Commit
	if _, _, err := lockWorkflowCodeRunTx(ctx, tx, runID); err != nil {
		return err
	}
	var status string
	var attempt int
	if err := tx.QueryRow(ctx, `SELECT status, attempt FROM workflow_steps WHERE run_id = $1 AND step_name = $2 FOR UPDATE`, runID, api.WorkflowCodeDriverStep).Scan(&status, &attempt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrWorkflowStepNotFound
		}
		return fmt.Errorf("pgstore: lock code workflow driver: %w", err)
	}
	if status != WorkflowStepStatusRunning || attempt != finish.Attempt {
		return fmt.Errorf("%w: code workflow invocation %d is not current", ErrConflict, finish.Attempt)
	}
	for _, op := range finish.Ops {
		if err := writeWorkflowCodeOpTx(ctx, tx, runID, op); err != nil {
			return err
		}
	}
	attemptStatus := WorkflowAttemptStatusSucceeded
	if finish.Error != nil {
		attemptStatus = WorkflowAttemptStatusFailed
		if finish.RetryAt != nil {
			attemptStatus = WorkflowAttemptStatusRetrying
		}
	}
	tag, err := tx.Exec(ctx, `
		UPDATE workflow_step_attempts
		SET status = $4, http_status = $5, finished_at = clock_timestamp(), next_attempt_at = $6, error = $7
		WHERE run_id = $1 AND step_name = $2 AND attempt = $3
	`, runID, api.WorkflowCodeDriverStep, finish.Attempt, attemptStatus, finish.HTTPStatus, finish.RetryAt, finish.Error)
	if err != nil {
		return fmt.Errorf("pgstore: finish code workflow driver attempt: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrWorkflowAttemptNotFound
	}
	if _, err := tx.Exec(ctx, `
		UPDATE workflow_steps
		SET status = 'pending', input = COALESCE($3, input), output = COALESCE($4, output),
		    error = $5, next_retry_at = $6
		WHERE run_id = $1 AND step_name = $2
	`, runID, api.WorkflowCodeDriverStep, nullableJSON(finish.DriverInput), nullableJSON(finish.Result), finish.Error, finish.RetryAt); err != nil {
		return fmt.Errorf("pgstore: settle code workflow driver: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE workflow_runs SET updated_at = now() WHERE id = $1`, runID); err != nil {
		return fmt.Errorf("pgstore: touch code workflow run: %w", err)
	}
	if err := syncOperationWorkflowTx(ctx, tx, runID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("pgstore: commit code workflow invocation: %w", err)
	}
	return nil
}

func writeWorkflowCodeOpTx(ctx context.Context, tx pgx.Tx, runID string, op WorkflowCodeOpWrite) error {
	finished := op.Status == WorkflowStepStatusSucceeded || op.Status == WorkflowStepStatusFailed
	if op.New {
		tag, err := tx.Exec(ctx, `
			INSERT INTO workflow_steps (run_id, step_name, status, attempt, input, output, error, started_at, finished_at, next_retry_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, now(), CASE WHEN $8 THEN now() END, $9)
			ON CONFLICT (run_id, step_name) DO NOTHING
		`, runID, op.StepName, op.Status, op.Attempt, op.Input, nullableJSON(op.Output), op.Error, finished, op.RetryAt)
		if err != nil {
			return fmt.Errorf("pgstore: record code operation %q: %w", op.StepName, err)
		}
		if tag.RowsAffected() == 0 {
			return fmt.Errorf("%w: code operation %q already exists", ErrConflict, op.StepName)
		}
	} else {
		tag, err := tx.Exec(ctx, `
			UPDATE workflow_steps
			SET status = $3, attempt = $4, output = $5, error = $6, next_retry_at = $7,
			    started_at = COALESCE(started_at, now()), finished_at = CASE WHEN $8 THEN now() END
			WHERE run_id = $1 AND step_name = $2 AND status IN ('pending', 'running', 'awaiting_event')
		`, runID, op.StepName, op.Status, op.Attempt, nullableJSON(op.Output), op.Error, op.RetryAt, finished)
		if err != nil {
			return fmt.Errorf("pgstore: update code operation %q: %w", op.StepName, err)
		}
		if tag.RowsAffected() == 0 {
			return fmt.Errorf("%w: code operation %q is not pending", ErrConflict, op.StepName)
		}
	}
	if op.Attempt == 0 {
		return nil
	}
	attemptStatus := workflowCodeAttemptStatus(op)
	if op.RetryAt != nil {
		attemptStatus = WorkflowAttemptStatusRetrying
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO workflow_step_attempts (run_id, step_name, attempt, status, finished_at, next_attempt_at, error)
		VALUES ($1, $2, $3, $4, clock_timestamp(), $5, $6)
		ON CONFLICT (run_id, step_name, attempt) DO UPDATE
		SET status = excluded.status, finished_at = excluded.finished_at,
		    next_attempt_at = excluded.next_attempt_at, error = excluded.error
	`, runID, op.StepName, op.Attempt, attemptStatus, op.RetryAt, op.Error); err != nil {
		return fmt.Errorf("pgstore: record code operation attempt %q: %w", op.StepName, err)
	}
	return nil
}

func (s *PgStore) ParkWorkflowCodeEvent(ctx context.Context, runID, stepName, eventName string, occurrence int, timeout time.Duration) (*WorkflowEvent, bool, time.Time, error) {
	if eventName == "" || occurrence < 0 || timeout <= 0 {
		return nil, false, time.Time{}, ErrWorkflowInvalidRecord
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, false, time.Time{}, fmt.Errorf("pgstore: begin park code workflow event: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck // no-op after Commit
	_, scheduledFor, err := lockWorkflowCodeRunTx(ctx, tx, runID)
	if err != nil {
		return nil, false, time.Time{}, err
	}
	var status string
	var startedAt *time.Time
	if err := tx.QueryRow(ctx, `SELECT status, started_at FROM workflow_steps WHERE run_id = $1 AND step_name = $2 FOR UPDATE`, runID, stepName).Scan(&status, &startedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, false, time.Time{}, ErrWorkflowStepNotFound
		}
		return nil, false, time.Time{}, fmt.Errorf("pgstore: lock code workflow event step: %w", err)
	}
	if status != WorkflowStepStatusPending && status != WorkflowStepStatusAwaitingEvent {
		return nil, false, time.Time{}, fmt.Errorf("%w: event wait step is not pending or awaiting", ErrConflict)
	}
	var matchDeadline *time.Time
	if startedAt != nil {
		value := startedAt.Add(timeout)
		matchDeadline = &value
	}
	query := fmt.Sprintf(`SELECT %s FROM workflow_events WHERE run_id = $1 AND event_name = $2 AND ($3::timestamptz IS NULL OR received_at < $3) ORDER BY received_at ASC, id ASC OFFSET $4 LIMIT 1`, workflowEventSelectCols)
	event, err := scanWorkflowEventCols(tx.QueryRow(ctx, query, runID, eventName, matchDeadline, occurrence).Scan)
	switch {
	case err == nil:
		if _, err := tx.Exec(ctx, `
			UPDATE workflow_steps
			SET status = 'succeeded', output = $3, started_at = COALESCE(started_at, now()), finished_at = now()
			WHERE run_id = $1 AND step_name = $2
		`, runID, stepName, []byte(event.Payload)); err != nil {
			return nil, false, time.Time{}, fmt.Errorf("pgstore: complete code workflow event wait: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, false, time.Time{}, fmt.Errorf("pgstore: commit code workflow event: %w", err)
		}
		return event, false, time.Time{}, nil
	case !errors.Is(err, pgx.ErrNoRows):
		return nil, false, time.Time{}, fmt.Errorf("pgstore: match code workflow event: %w", err)
	}
	var now time.Time
	if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return nil, false, time.Time{}, fmt.Errorf("pgstore: read code workflow event clock: %w", err)
	}
	if matchDeadline != nil && !now.Before(*matchDeadline) {
		if _, err := tx.Exec(ctx, `UPDATE workflow_steps SET status = 'succeeded', output = NULL, finished_at = $3 WHERE run_id = $1 AND step_name = $2`, runID, stepName, now); err != nil {
			return nil, false, time.Time{}, fmt.Errorf("pgstore: time out code workflow event wait: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, false, time.Time{}, fmt.Errorf("pgstore: commit code workflow event timeout: %w", err)
		}
		return nil, true, time.Time{}, nil
	}
	var started time.Time
	if err := tx.QueryRow(ctx, `
		UPDATE workflow_steps
		SET status = 'awaiting_event', started_at = COALESCE(started_at, clock_timestamp())
		WHERE run_id = $1 AND step_name = $2
		RETURNING started_at
	`, runID, stepName).Scan(&started); err != nil {
		return nil, false, time.Time{}, fmt.Errorf("pgstore: park code workflow event wait: %w", err)
	}
	deadline := started.Add(timeout)
	if _, err := tx.Exec(ctx, `
		UPDATE workflow_runs
		SET status = 'awaiting_event', current_step = $2, scheduled_for = $3, updated_at = now()
		WHERE id = $1
	`, runID, stepName, earlierWorkflowWake(scheduledFor, deadline, now)); err != nil {
		return nil, false, time.Time{}, fmt.Errorf("pgstore: schedule code workflow event wait: %w", err)
	}
	if err := syncOperationWorkflowTx(ctx, tx, runID); err != nil {
		return nil, false, time.Time{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, false, time.Time{}, fmt.Errorf("pgstore: commit code workflow event wait: %w", err)
	}
	return nil, false, deadline, nil
}
