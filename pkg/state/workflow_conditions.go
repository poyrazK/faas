package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

const (
	WorkflowConditionReady     = "ready"
	WorkflowConditionWaiting   = "waiting"
	WorkflowConditionSucceeded = "succeeded"
	WorkflowConditionTimedOut  = "timed_out"
)

// WorkflowConditionUpdate either inspects a parked condition (Checked=false)
// or commits one completed checker invocation. A false result schedules the
// next check without retaining compute; timeout and attempt exhaustion share
// the existing on_timeout workflow branch.
type WorkflowConditionUpdate struct {
	RunID, StepName string
	Checked, Done   bool
	Result          json.RawMessage
	Error           *string
	HTTPStatus      *int
	Interval        time.Duration
	Timeout         time.Duration
	MaxAttempts     int
	OnTimeout       bool
}

type WorkflowConditionOutcome struct {
	Status      string
	NextCheckAt time.Time
}

func validateWorkflowConditionUpdate(u WorkflowConditionUpdate) error {
	if u.RunID == "" || u.StepName == "" || u.Interval <= 0 || u.Timeout <= 0 || u.MaxAttempts < 1 || u.MaxAttempts > 1000 {
		return ErrWorkflowInvalidRecord
	}
	if u.Checked {
		if err := validateWorkflowJSON(u.Result, true); err != nil {
			return err
		}
		if err := validateWorkflowHTTPStatus(u.HTTPStatus); err != nil {
			return err
		}
	}
	return nil
}

func workflowConditionWake(now, startedAt time.Time, interval, timeout time.Duration) time.Time {
	next := now.Add(interval)
	deadline := startedAt.Add(timeout)
	if deadline.Before(next) {
		return deadline
	}
	return next
}

func (m *MemStore) ResolveWorkflowCondition(_ context.Context, u WorkflowConditionUpdate) (WorkflowConditionOutcome, error) {
	if err := validateWorkflowConditionUpdate(u); err != nil {
		return WorkflowConditionOutcome{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	run, ok := m.workflowRuns[u.RunID]
	if !ok {
		return WorkflowConditionOutcome{}, ErrWorkflowRunNotFound
	}
	if run.Status == WorkflowRunStatusSucceeded || run.Status == WorkflowRunStatusFailed || run.Status == WorkflowRunStatusDead {
		return WorkflowConditionOutcome{}, ErrConflict
	}
	step, ok := m.workflowSteps[u.RunID][u.StepName]
	if !ok {
		return WorkflowConditionOutcome{}, ErrWorkflowStepNotFound
	}
	now := time.Now().UTC()
	if !u.Checked {
		if step.Status != WorkflowStepStatusPending && step.Status != WorkflowStepStatusAwaitingEvent {
			return WorkflowConditionOutcome{}, ErrConflict
		}
		if step.StartedAt == nil {
			return WorkflowConditionOutcome{Status: WorkflowConditionReady}, nil
		}
		if !now.Before(step.StartedAt.Add(u.Timeout)) {
			return m.expireWorkflowConditionLocked(run, step, u, now), nil
		}
		if step.Status == WorkflowStepStatusPending || step.NextCheckAt == nil || !now.Before(*step.NextCheckAt) {
			return WorkflowConditionOutcome{Status: WorkflowConditionReady}, nil
		}
		wake := earlierWorkflowWake(run.ScheduledFor, *step.NextCheckAt, now)
		run.Status = WorkflowRunStatusAwaitingEvent
		run.ScheduledFor = wake
		run.UpdatedAt = now
		m.workflowRuns[u.RunID] = run
		return WorkflowConditionOutcome{Status: WorkflowConditionWaiting, NextCheckAt: *step.NextCheckAt}, nil
	}
	if step.Status != WorkflowStepStatusRunning || step.StartedAt == nil || step.Attempt < 1 {
		return WorkflowConditionOutcome{}, ErrConflict
	}
	attemptStatus := WorkflowAttemptStatusSucceeded
	if u.Error != nil {
		attemptStatus = WorkflowAttemptStatusFailed
	}
	step.Output = cloneWorkflowJSON(u.Result)
	step.Error = u.Error
	step.NextCheckAt = nil
	if !now.Before(step.StartedAt.Add(u.Timeout)) {
		if err := m.finishWorkflowConditionAttemptLocked(u, attemptStatus, nil, now); err != nil {
			return WorkflowConditionOutcome{}, err
		}
		return m.expireWorkflowConditionLocked(run, step, u, now), nil
	}
	if u.Done {
		if err := m.finishWorkflowConditionAttemptLocked(u, WorkflowAttemptStatusSucceeded, nil, now); err != nil {
			return WorkflowConditionOutcome{}, err
		}
		step.Status = WorkflowStepStatusSucceeded
		step.FinishedAt = &now
		run.Status = WorkflowRunStatusPending
		run.ScheduledFor = now
		m.workflowSteps[u.RunID][u.StepName] = step
		m.workflowRuns[u.RunID] = run
		return WorkflowConditionOutcome{Status: WorkflowConditionSucceeded}, nil
	}
	if step.Attempt >= u.MaxAttempts || !now.Before(step.StartedAt.Add(u.Timeout)) {
		if err := m.finishWorkflowConditionAttemptLocked(u, attemptStatus, nil, now); err != nil {
			return WorkflowConditionOutcome{}, err
		}
		return m.expireWorkflowConditionLocked(run, step, u, now), nil
	}
	next := workflowConditionWake(now, *step.StartedAt, u.Interval, u.Timeout)
	if u.Error != nil {
		attemptStatus = WorkflowAttemptStatusRetrying
	}
	if err := m.finishWorkflowConditionAttemptLocked(u, attemptStatus, &next, now); err != nil {
		return WorkflowConditionOutcome{}, err
	}
	step.Status = WorkflowStepStatusAwaitingEvent
	step.NextCheckAt = &next
	m.workflowSteps[u.RunID][u.StepName] = step
	wake := earlierWorkflowWake(run.ScheduledFor, next, now)
	run.Status = WorkflowRunStatusAwaitingEvent
	run.CurrentStep = &u.StepName
	run.ScheduledFor = wake
	run.UpdatedAt = now
	m.workflowRuns[u.RunID] = run
	return WorkflowConditionOutcome{Status: WorkflowConditionWaiting, NextCheckAt: next}, nil
}

func (m *MemStore) finishWorkflowConditionAttemptLocked(u WorkflowConditionUpdate, status string, next *time.Time, now time.Time) error {
	key := workflowStepAttemptKey{runID: u.RunID, stepName: u.StepName, attempt: m.workflowSteps[u.RunID][u.StepName].Attempt}
	attempt, ok := m.workflowStepAttempts[key]
	if !ok {
		return ErrWorkflowAttemptNotFound
	}
	attempt.Status = status
	attempt.HTTPStatus = cloneWorkflowInt(u.HTTPStatus)
	attempt.FinishedAt = &now
	attempt.NextAttemptAt = cloneWorkflowTime(next)
	attempt.Error = cloneWorkflowString(u.Error)
	m.workflowStepAttempts[key] = attempt
	return nil
}

func (m *MemStore) expireWorkflowConditionLocked(run WorkflowRun, step WorkflowStep, u WorkflowConditionUpdate, now time.Time) WorkflowConditionOutcome {
	step.NextCheckAt = nil
	step.FinishedAt = &now
	run.CurrentStep = &u.StepName
	run.UpdatedAt = now
	if u.OnTimeout {
		step.Status = WorkflowStepStatusSucceeded
		step.Output = json.RawMessage(`{"timeout":true}`)
		run.Status = WorkflowRunStatusPending
		run.ScheduledFor = now
	} else {
		message := "workflow condition timed out or exhausted its check attempts"
		step.Status = WorkflowStepStatusDead
		step.Error = &message
		run.Status = WorkflowRunStatusDead
		run.LastError = &message
		run.FinishedAt = &now
	}
	m.workflowSteps[u.RunID][u.StepName] = step
	m.workflowRuns[u.RunID] = run
	return WorkflowConditionOutcome{Status: WorkflowConditionTimedOut}
}

func (s *PgStore) ResolveWorkflowCondition(ctx context.Context, u WorkflowConditionUpdate) (WorkflowConditionOutcome, error) {
	if err := validateWorkflowConditionUpdate(u); err != nil {
		return WorkflowConditionOutcome{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return WorkflowConditionOutcome{}, fmt.Errorf("pgstore: begin condition transition: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var runStatus string
	var scheduledFor time.Time
	err = tx.QueryRow(ctx, `SELECT status, scheduled_for FROM workflow_runs WHERE id = $1 FOR UPDATE`, u.RunID).Scan(&runStatus, &scheduledFor)
	if errors.Is(err, pgx.ErrNoRows) {
		return WorkflowConditionOutcome{}, ErrWorkflowRunNotFound
	}
	if err != nil {
		return WorkflowConditionOutcome{}, fmt.Errorf("pgstore: lock condition run: %w", err)
	}
	if runStatus == WorkflowRunStatusSucceeded || runStatus == WorkflowRunStatusFailed || runStatus == WorkflowRunStatusDead {
		return WorkflowConditionOutcome{}, ErrConflict
	}
	var status string
	var attempt int
	var startedAt, nextCheckAt *time.Time
	err = tx.QueryRow(ctx, `SELECT status, attempt, started_at, next_check_at FROM workflow_steps WHERE run_id = $1 AND step_name = $2 FOR UPDATE`, u.RunID, u.StepName).Scan(&status, &attempt, &startedAt, &nextCheckAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return WorkflowConditionOutcome{}, ErrWorkflowStepNotFound
	}
	if err != nil {
		return WorkflowConditionOutcome{}, fmt.Errorf("pgstore: lock condition step: %w", err)
	}
	var now time.Time
	if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return WorkflowConditionOutcome{}, fmt.Errorf("pgstore: condition clock: %w", err)
	}
	if !u.Checked {
		if status != WorkflowStepStatusPending && status != WorkflowStepStatusAwaitingEvent {
			return WorkflowConditionOutcome{}, ErrConflict
		}
		if startedAt == nil {
			return WorkflowConditionOutcome{Status: WorkflowConditionReady}, tx.Commit(ctx)
		}
		if !now.Before(startedAt.Add(u.Timeout)) {
			return resolveExpiredPgCondition(ctx, tx, u, now)
		}
		if status == WorkflowStepStatusPending || nextCheckAt == nil || !now.Before(*nextCheckAt) {
			return WorkflowConditionOutcome{Status: WorkflowConditionReady}, tx.Commit(ctx)
		}
		wake := earlierWorkflowWake(scheduledFor, *nextCheckAt, now)
		if _, err := tx.Exec(ctx, `UPDATE workflow_runs SET status = 'awaiting_event', scheduled_for = $2, updated_at = $3 WHERE id = $1`, u.RunID, wake, now); err != nil {
			return WorkflowConditionOutcome{}, fmt.Errorf("pgstore: repark condition: %w", err)
		}
		return WorkflowConditionOutcome{Status: WorkflowConditionWaiting, NextCheckAt: *nextCheckAt}, tx.Commit(ctx)
	}
	if status != WorkflowStepStatusRunning || startedAt == nil || attempt < 1 {
		return WorkflowConditionOutcome{}, ErrConflict
	}
	attemptStatus := WorkflowAttemptStatusSucceeded
	if u.Error != nil {
		attemptStatus = WorkflowAttemptStatusFailed
	}
	if !now.Before(startedAt.Add(u.Timeout)) {
		if err := finishPgWorkflowConditionAttempt(ctx, tx, u, attempt, attemptStatus, nil, now); err != nil {
			return WorkflowConditionOutcome{}, err
		}
		return resolveExpiredPgCondition(ctx, tx, u, now)
	}
	if u.Done {
		if err := finishPgWorkflowConditionAttempt(ctx, tx, u, attempt, WorkflowAttemptStatusSucceeded, nil, now); err != nil {
			return WorkflowConditionOutcome{}, err
		}
		if _, err := tx.Exec(ctx, `UPDATE workflow_steps SET status = 'succeeded', output = $3, error = $4, next_check_at = NULL, finished_at = $5 WHERE run_id = $1 AND step_name = $2`, u.RunID, u.StepName, u.Result, u.Error, now); err != nil {
			return WorkflowConditionOutcome{}, fmt.Errorf("pgstore: complete condition step: %w", err)
		}
		if _, err := tx.Exec(ctx, `UPDATE workflow_runs SET status = 'pending', current_step = $2, scheduled_for = $3, updated_at = $3 WHERE id = $1`, u.RunID, u.StepName, now); err != nil {
			return WorkflowConditionOutcome{}, fmt.Errorf("pgstore: complete condition run: %w", err)
		}
		return WorkflowConditionOutcome{Status: WorkflowConditionSucceeded}, tx.Commit(ctx)
	}
	if attempt >= u.MaxAttempts || !now.Before(startedAt.Add(u.Timeout)) {
		if err := finishPgWorkflowConditionAttempt(ctx, tx, u, attempt, attemptStatus, nil, now); err != nil {
			return WorkflowConditionOutcome{}, err
		}
		return resolveExpiredPgCondition(ctx, tx, u, now)
	}
	next := workflowConditionWake(now, *startedAt, u.Interval, u.Timeout)
	if u.Error != nil {
		attemptStatus = WorkflowAttemptStatusRetrying
	}
	if err := finishPgWorkflowConditionAttempt(ctx, tx, u, attempt, attemptStatus, &next, now); err != nil {
		return WorkflowConditionOutcome{}, err
	}
	wake := earlierWorkflowWake(scheduledFor, next, now)
	if _, err := tx.Exec(ctx, `UPDATE workflow_steps SET status = 'awaiting_event', output = $3, error = $4, next_check_at = $5 WHERE run_id = $1 AND step_name = $2`, u.RunID, u.StepName, u.Result, u.Error, next); err != nil {
		return WorkflowConditionOutcome{}, fmt.Errorf("pgstore: park condition step: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE workflow_runs SET status = 'awaiting_event', current_step = $2, scheduled_for = $3, updated_at = $4 WHERE id = $1`, u.RunID, u.StepName, wake, now); err != nil {
		return WorkflowConditionOutcome{}, fmt.Errorf("pgstore: park condition run: %w", err)
	}
	return WorkflowConditionOutcome{Status: WorkflowConditionWaiting, NextCheckAt: next}, tx.Commit(ctx)
}

func finishPgWorkflowConditionAttempt(ctx context.Context, tx pgx.Tx, u WorkflowConditionUpdate, attempt int, status string, next *time.Time, now time.Time) error {
	tag, err := tx.Exec(ctx, `
		UPDATE workflow_step_attempts
		SET status = $4, http_status = $5, finished_at = $6,
		    next_attempt_at = $7, error = $8
		WHERE run_id = $1 AND step_name = $2 AND attempt = $3
	`, u.RunID, u.StepName, attempt, status, u.HTTPStatus, now, next, u.Error)
	if err != nil {
		return fmt.Errorf("pgstore: finish condition checker attempt: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrWorkflowAttemptNotFound
	}
	return nil
}

func resolveExpiredPgCondition(ctx context.Context, tx pgx.Tx, u WorkflowConditionUpdate, now time.Time) (WorkflowConditionOutcome, error) {
	if u.OnTimeout {
		if _, err := tx.Exec(ctx, `UPDATE workflow_steps SET status = 'succeeded', output = '{"timeout":true}'::jsonb, next_check_at = NULL, finished_at = $3 WHERE run_id = $1 AND step_name = $2`, u.RunID, u.StepName, now); err != nil {
			return WorkflowConditionOutcome{}, fmt.Errorf("pgstore: timeout condition step: %w", err)
		}
		if _, err := tx.Exec(ctx, `UPDATE workflow_runs SET status = 'pending', current_step = $2, scheduled_for = $3, updated_at = $3 WHERE id = $1`, u.RunID, u.StepName, now); err != nil {
			return WorkflowConditionOutcome{}, fmt.Errorf("pgstore: schedule condition timeout handler: %w", err)
		}
	} else {
		message := "workflow condition timed out or exhausted its check attempts"
		if _, err := tx.Exec(ctx, `UPDATE workflow_steps SET status = 'dead', error = $3, output = COALESCE($5, output), next_check_at = NULL, finished_at = $4 WHERE run_id = $1 AND step_name = $2`, u.RunID, u.StepName, message, now, u.Result); err != nil {
			return WorkflowConditionOutcome{}, fmt.Errorf("pgstore: close condition step: %w", err)
		}
		if _, err := tx.Exec(ctx, `UPDATE workflow_runs SET status = 'dead', current_step = $2, last_error = $3, finished_at = $4, updated_at = $4 WHERE id = $1`, u.RunID, u.StepName, message, now); err != nil {
			return WorkflowConditionOutcome{}, fmt.Errorf("pgstore: close condition run: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return WorkflowConditionOutcome{}, fmt.Errorf("pgstore: commit condition timeout: %w", err)
	}
	return WorkflowConditionOutcome{Status: WorkflowConditionTimedOut}, nil
}
