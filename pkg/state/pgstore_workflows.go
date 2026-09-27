package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const workflowRunSelectCols = `id, app_id, workflow_name, status, current_step, input, output,
       definition_snapshot, scheduled_for, started_at, finished_at, last_error, created_at, updated_at`

const workflowStepSelectCols = `run_id, step_name, status, attempt, input, output,
       started_at, next_check_at, next_retry_at, finished_at, error, created_at`

const workflowEventSelectCols = `id, run_id, event_name, payload, received_at`

func scanWorkflowRunCols(scan func(...any) error) (*WorkflowRun, error) {
	var r WorkflowRun
	var inputBytes, outputBytes, defBytes []byte
	var runUUID string
	if err := scan(&runUUID, &r.AppID, &r.WorkflowName, &r.Status, &r.CurrentStep,
		&inputBytes, &outputBytes, &defBytes, &r.ScheduledFor, &r.StartedAt,
		&r.FinishedAt, &r.LastError, &r.CreatedAt, &r.UpdatedAt); err != nil {
		return nil, err
	}
	r.ID = runUUID
	if len(inputBytes) > 0 {
		r.Input = json.RawMessage(inputBytes)
	}
	if len(outputBytes) > 0 {
		r.Output = json.RawMessage(outputBytes)
	}
	if len(defBytes) > 0 {
		r.DefinitionSnapshot = json.RawMessage(defBytes)
	}
	return &r, nil
}

func scanWorkflowStepCols(scan func(...any) error) (*WorkflowStep, error) {
	var s WorkflowStep
	var inputBytes, outputBytes []byte
	var runUUID string
	if err := scan(&runUUID, &s.StepName, &s.Status, &s.Attempt,
		&inputBytes, &outputBytes, &s.StartedAt, &s.NextCheckAt, &s.NextRetryAt, &s.FinishedAt,
		&s.Error, &s.CreatedAt); err != nil {
		return nil, err
	}
	s.RunID = runUUID
	if len(inputBytes) > 0 {
		s.Input = json.RawMessage(inputBytes)
	}
	if len(outputBytes) > 0 {
		s.Output = json.RawMessage(outputBytes)
	}
	return &s, nil
}

func scanWorkflowEventCols(scan func(...any) error) (*WorkflowEvent, error) {
	var e WorkflowEvent
	var payloadBytes []byte
	var idUUID, runUUID string
	if err := scan(&idUUID, &runUUID, &e.EventName, &payloadBytes, &e.ReceivedAt); err != nil {
		return nil, err
	}
	e.ID = idUUID
	e.RunID = runUUID
	if len(payloadBytes) > 0 {
		e.Payload = json.RawMessage(payloadBytes)
	}
	return &e, nil
}

func (s *PgStore) CreateWorkflowRun(ctx context.Context, r *WorkflowRun) error {
	if err := prepareWorkflowRun(r); err != nil {
		return err
	}
	return insertWorkflowRun(ctx, s.pool, r)
}

type workflowRunQueryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func prepareWorkflowRun(r *WorkflowRun) error {
	if r == nil {
		return fmt.Errorf("%w: nil run", ErrWorkflowInvalidRecord)
	}
	if r.ID == "" {
		r.ID = uuid.NewString()
	}
	if r.Status == "" {
		r.Status = WorkflowRunStatusPending
	}
	if err := validateWorkflowRunStatus(r.Status); err != nil {
		return err
	}
	if r.ScheduledFor.IsZero() {
		r.ScheduledFor = time.Now().UTC()
	}
	if len(r.Input) == 0 {
		r.Input = json.RawMessage("{}")
	}
	if err := validateWorkflowJSON(r.Input, true); err != nil {
		return err
	}
	if err := validateWorkflowJSON(r.DefinitionSnapshot, true); err != nil {
		return err
	}
	return nil
}

func insertWorkflowRun(ctx context.Context, q workflowRunQueryer, r *WorkflowRun) error {
	query := `
		INSERT INTO workflow_runs (
			id, app_id, workflow_name, status, input, definition_snapshot, scheduled_for
		) VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING created_at, updated_at
	`
	err := q.QueryRow(ctx, query,
		r.ID, r.AppID, r.WorkflowName, r.Status, r.Input, r.DefinitionSnapshot, r.ScheduledFor,
	).Scan(&r.CreatedAt, &r.UpdatedAt)
	if err != nil {
		return fmt.Errorf("pgstore: create workflow run: %w", err)
	}
	return nil
}

// CreateWorkflowRunAdmitted holds a transaction-scoped advisory lock keyed by
// app before checking and consuming the active-run quota. This closes the
// count-then-insert race between concurrent apid requests and replicas.
func (s *PgStore) CreateWorkflowRunAdmitted(ctx context.Context, r *WorkflowRun, maxActive int) (int, error) {
	if maxActive < 1 {
		return 0, ErrWorkflowRunQuotaExceeded
	}
	if err := prepareWorkflowRun(r); err != nil {
		return 0, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("pgstore: begin workflow admission: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck // no-op after Commit
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, r.AppID); err != nil {
		return 0, fmt.Errorf("pgstore: lock workflow admission: %w", err)
	}
	var active int
	if err := tx.QueryRow(ctx, `
		SELECT count(*) FROM workflow_runs
		WHERE app_id = $1 AND status IN ('pending', 'running', 'awaiting_event')
	`, r.AppID).Scan(&active); err != nil {
		return 0, fmt.Errorf("pgstore: count workflow admission: %w", err)
	}
	if active >= maxActive {
		return active, ErrWorkflowRunQuotaExceeded
	}
	if err := insertWorkflowRun(ctx, tx, r); err != nil {
		return active, err
	}
	if err := tx.Commit(ctx); err != nil {
		return active, fmt.Errorf("pgstore: commit workflow admission: %w", err)
	}
	return active + 1, nil
}

func (s *PgStore) GetWorkflowRun(ctx context.Context, id string) (*WorkflowRun, error) {
	query := fmt.Sprintf(`SELECT %s FROM workflow_runs WHERE id = $1`, workflowRunSelectCols)
	row := s.pool.QueryRow(ctx, query, id)
	r, err := scanWorkflowRunCols(row.Scan)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrWorkflowRunNotFound
		}
		return nil, fmt.Errorf("pgstore: get workflow run %s: %w", id, err)
	}
	return r, nil
}

func (s *PgStore) ListWorkflowRuns(ctx context.Context, appID string, opts ListWorkflowRunsOpts) ([]*WorkflowRun, int, error) {
	if opts.Offset < 0 || opts.Limit < 0 {
		return nil, 0, ErrWorkflowInvalidPagination
	}
	if opts.Status != "" {
		if err := validateWorkflowRunStatus(opts.Status); err != nil {
			return nil, 0, err
		}
	}
	countQuery := `SELECT count(*) FROM workflow_runs WHERE app_id = $1`
	var args []any
	args = append(args, appID)
	if opts.Status != "" {
		countQuery += ` AND status = $2`
		args = append(args, opts.Status)
	}

	var total int
	if err := s.pool.QueryRow(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("pgstore: count workflow runs: %w", err)
	}

	query := fmt.Sprintf(`SELECT %s FROM workflow_runs WHERE app_id = $1`, workflowRunSelectCols)
	var qArgs []any
	qArgs = append(qArgs, appID)
	argIdx := 2
	if opts.Status != "" {
		query += fmt.Sprintf(` AND status = $%d`, argIdx)
		qArgs = append(qArgs, opts.Status)
		argIdx++
	}

	query += ` ORDER BY created_at DESC, id DESC`
	if opts.Limit > 0 {
		query += fmt.Sprintf(` LIMIT $%d`, argIdx)
		qArgs = append(qArgs, opts.Limit)
		argIdx++
	}
	if opts.Offset > 0 {
		query += fmt.Sprintf(` OFFSET $%d`, argIdx)
		qArgs = append(qArgs, opts.Offset)
	}

	rows, err := s.pool.Query(ctx, query, qArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("pgstore: list workflow runs: %w", err)
	}
	defer rows.Close()

	var runs []*WorkflowRun
	for rows.Next() {
		r, err := scanWorkflowRunCols(rows.Scan)
		if err != nil {
			return nil, 0, fmt.Errorf("pgstore: scan workflow run: %w", err)
		}
		runs = append(runs, r)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("pgstore: list workflow runs rows: %w", err)
	}

	return runs, total, nil
}

func (s *PgStore) MarkWorkflowRunStatus(ctx context.Context, id, status string, output json.RawMessage, lastErr *string) error {
	if err := validateWorkflowRunStatus(status); err != nil {
		return err
	}
	if err := validateWorkflowJSON(output, false); err != nil {
		return err
	}
	query := `
		UPDATE workflow_runs
		SET status = $2,
		    output = COALESCE($3, output),
		    last_error = COALESCE($4, last_error),
		    started_at = CASE WHEN $2 = 'running' AND started_at IS NULL THEN now() ELSE started_at END,
		    finished_at = CASE WHEN $2 IN ('succeeded', 'failed', 'dead') THEN now() ELSE finished_at END,
		    updated_at = now()
			WHERE id = $1
			  AND (status NOT IN ('succeeded', 'failed', 'dead') OR status = $2)
	`
	tag, err := s.pool.Exec(ctx, query, id, status, output, lastErr)
	if err != nil {
		return fmt.Errorf("pgstore: mark workflow run status: %w", err)
	}
	if tag.RowsAffected() == 0 {
		var exists bool
		if err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM workflow_runs WHERE id = $1)`, id).Scan(&exists); err != nil {
			return fmt.Errorf("pgstore: inspect workflow transition conflict: %w", err)
		}
		if !exists {
			return ErrWorkflowRunNotFound
		}
		return fmt.Errorf("%w: workflow run is terminal", ErrConflict)
	}
	return nil
}

func (s *PgStore) ClaimNextPendingRun(ctx context.Context) (*WorkflowRun, error) {
	query := fmt.Sprintf(`
		UPDATE workflow_runs
		SET status = 'running',
		    started_at = COALESCE(started_at, now()),
		    updated_at = now()
		WHERE id = (
			SELECT id FROM workflow_runs
			WHERE status = 'pending' AND scheduled_for <= now()
			ORDER BY scheduled_for ASC, id ASC
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		)
		RETURNING %s
	`, workflowRunSelectCols)

	row := s.pool.QueryRow(ctx, query)
	r, err := scanWorkflowRunCols(row.Scan)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("pgstore: claim next pending run: %w", err)
	}
	return r, nil
}

// ClaimNextDueWorkflowRun claims either a newly queued run or a parked wait
// whose deadline has arrived. FOR UPDATE SKIP LOCKED keeps multiple
// schedd workers from dispatching the same run.
func (s *PgStore) ClaimNextDueWorkflowRun(ctx context.Context) (*WorkflowRun, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("pgstore: begin workflow claim: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck // no-op after Commit

	var id, priorStatus string
	err = tx.QueryRow(ctx, `
		SELECT id, status FROM workflow_runs
		WHERE (status IN ('pending', 'awaiting_event') AND scheduled_for <= now())
		   OR (status = 'running' AND updated_at <= now() - ($1::bigint * interval '1 millisecond'))
		ORDER BY CASE WHEN status = 'running' THEN updated_at ELSE scheduled_for END ASC, id ASC
		FOR UPDATE SKIP LOCKED
		LIMIT 1
	`, int64(WorkflowRunStaleAfter/time.Millisecond)).Scan(&id, &priorStatus)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("pgstore: select next due workflow run: %w", err)
	}

	query := fmt.Sprintf(`
		UPDATE workflow_runs
		SET status = 'running',
		    started_at = COALESCE(started_at, now()),
		    updated_at = now()
		WHERE id = $1
		RETURNING %s
	`, workflowRunSelectCols)
	r, err := scanWorkflowRunCols(tx.QueryRow(ctx, query, id).Scan)
	if err != nil {
		return nil, fmt.Errorf("pgstore: claim next due workflow run: %w", err)
	}
	if priorStatus == WorkflowRunStatusRunning {
		if _, err := tx.Exec(ctx, `
			UPDATE workflow_steps
			SET status = 'pending', attempt = GREATEST(attempt - 1, 0),
			    finished_at = NULL, error = NULL, next_retry_at = NULL
			WHERE run_id = $1 AND status = 'running'
		`, id); err != nil {
			return nil, fmt.Errorf("pgstore: recover stale workflow steps: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("pgstore: commit workflow claim: %w", err)
	}
	return r, nil
}

// ScheduleWorkflowRun updates the run's scheduler state and next due time.
func (s *PgStore) ScheduleWorkflowRun(ctx context.Context, id, status string, scheduledFor time.Time) error {
	if err := validateWorkflowRunStatus(status); err != nil {
		return err
	}
	tag, err := s.pool.Exec(ctx, `
			UPDATE workflow_runs
			SET status = $2,
			    scheduled_for = CASE
			      WHEN status = 'awaiting_event' AND $2 = 'awaiting_event'
			      THEN LEAST(scheduled_for, $3)
			      ELSE $3 END,
			    updated_at = now()
			WHERE id = $1
			  AND (status NOT IN ('succeeded', 'failed', 'dead') OR status = $2)
		`, id, status, scheduledFor.UTC())
	if err != nil {
		return fmt.Errorf("pgstore: schedule workflow run: %w", err)
	}
	if tag.RowsAffected() == 0 {
		var exists bool
		if err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM workflow_runs WHERE id = $1)`, id).Scan(&exists); err != nil {
			return fmt.Errorf("pgstore: inspect workflow schedule conflict: %w", err)
		}
		if !exists {
			return ErrWorkflowRunNotFound
		}
		return fmt.Errorf("%w: workflow run is terminal", ErrConflict)
	}
	return nil
}

func (s *PgStore) SetWorkflowRunWake(ctx context.Context, id, status string, scheduledFor time.Time) error {
	if status != WorkflowRunStatusPending && status != WorkflowRunStatusAwaitingEvent {
		return ErrWorkflowInvalidRecord
	}
	tag, err := s.pool.Exec(ctx, `UPDATE workflow_runs SET status = $2, scheduled_for = $3, updated_at = now() WHERE id = $1 AND status IN ('running', 'pending', 'awaiting_event') AND NOT (status = 'pending' AND scheduled_for <= now())`, id, status, scheduledFor.UTC())
	if err != nil {
		return fmt.Errorf("pgstore: set workflow wake: %w", err)
	}
	if tag.RowsAffected() == 0 {
		var exists bool
		if err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM workflow_runs WHERE id = $1)`, id).Scan(&exists); err != nil {
			return fmt.Errorf("pgstore: inspect workflow wake: %w", err)
		}
		if !exists {
			return ErrWorkflowRunNotFound
		}
	}
	return nil
}

// RecoverWorkflowRun returns a non-terminal orchestration attempt to the due
// queue. Any ambiguous running step is retried with the same attempt number so
// its Idempotency-Key remains stable across a crash or persistence failure.
func (s *PgStore) RecoverWorkflowRun(ctx context.Context, id string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("pgstore: begin recover workflow: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck // no-op after Commit
	if _, err := tx.Exec(ctx, `
		UPDATE workflow_steps
		SET status = 'pending', attempt = GREATEST(attempt - 1, 0),
		    finished_at = NULL, error = NULL, next_retry_at = NULL
		WHERE run_id = $1 AND status = 'running'
	`, id); err != nil {
		return fmt.Errorf("pgstore: recover workflow steps: %w", err)
	}
	tag, err := tx.Exec(ctx, `
		UPDATE workflow_runs
		SET status = 'pending', scheduled_for = now(), updated_at = now()
		WHERE id = $1 AND status NOT IN ('succeeded', 'failed', 'dead')
	`, id)
	if err != nil {
		return fmt.Errorf("pgstore: recover workflow run: %w", err)
	}
	if tag.RowsAffected() == 0 {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM workflow_runs WHERE id = $1)`, id).Scan(&exists); err != nil {
			return fmt.Errorf("pgstore: inspect workflow recovery: %w", err)
		}
		if !exists {
			return ErrWorkflowRunNotFound
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("pgstore: commit workflow recovery: %w", err)
	}
	return nil
}

// CancelWorkflowRun atomically makes the run terminal and skips every active
// step. Locking the run prevents a concurrent scheduler completion from
// leaving a partially cancelled DAG.
func (s *PgStore) CancelWorkflowRun(ctx context.Context, id, reason string) (*WorkflowRun, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("pgstore: begin cancel workflow: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck // no-op after Commit
	selectQuery := fmt.Sprintf(`SELECT %s FROM workflow_runs WHERE id = $1 FOR UPDATE`, workflowRunSelectCols)
	run, err := scanWorkflowRunCols(tx.QueryRow(ctx, selectQuery, id).Scan)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrWorkflowRunNotFound
		}
		return nil, fmt.Errorf("pgstore: lock workflow for cancellation: %w", err)
	}
	if run.Status == WorkflowRunStatusSucceeded || run.Status == WorkflowRunStatusFailed || run.Status == WorkflowRunStatusDead {
		if err := tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("pgstore: commit terminal workflow cancellation: %w", err)
		}
		return run, nil
	}
	if _, err := tx.Exec(ctx, `
		UPDATE workflow_steps
		SET status = 'skipped', error = $2, finished_at = now(), next_retry_at = NULL
		WHERE run_id = $1 AND status IN ('pending', 'running', 'awaiting_event')
	`, id, reason); err != nil {
		return nil, fmt.Errorf("pgstore: skip workflow steps for cancellation: %w", err)
	}
	updateQuery := fmt.Sprintf(`
		UPDATE workflow_runs
		SET status = 'failed', last_error = $2, finished_at = now(), updated_at = now()
		WHERE id = $1
		RETURNING %s
	`, workflowRunSelectCols)
	run, err = scanWorkflowRunCols(tx.QueryRow(ctx, updateQuery, id, reason).Scan)
	if err != nil {
		return nil, fmt.Errorf("pgstore: mark workflow cancelled: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("pgstore: commit workflow cancellation: %w", err)
	}
	return run, nil
}

func (s *PgStore) CountActiveRunsByApp(ctx context.Context, appID string) (int, error) {
	query := `
		SELECT count(*) FROM workflow_runs
		WHERE app_id = $1 AND status IN ('pending', 'running', 'awaiting_event')
	`
	var count int
	if err := s.pool.QueryRow(ctx, query, appID).Scan(&count); err != nil {
		return 0, fmt.Errorf("pgstore: count active runs: %w", err)
	}
	return count, nil
}

func (s *PgStore) CreateWorkflowSteps(ctx context.Context, runID string, steps []*WorkflowStep) error {
	if len(steps) == 0 {
		return nil
	}
	for _, step := range steps {
		if step == nil {
			return fmt.Errorf("%w: nil step", ErrWorkflowInvalidRecord)
		}
		if step.StepName == "" {
			return fmt.Errorf("%w: step name is empty", ErrWorkflowInvalidRecord)
		}
		status := step.Status
		if status == "" {
			status = WorkflowStepStatusPending
		}
		if err := validateWorkflowStepStatus(status); err != nil {
			return err
		}
		if step.Attempt < 0 {
			return ErrWorkflowInvalidAttempt
		}
		if err := validateWorkflowJSON(step.Input, false); err != nil {
			return err
		}
	}
	var exists bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM workflow_runs WHERE id = $1)`, runID).Scan(&exists); err != nil {
		return fmt.Errorf("pgstore: check workflow run: %w", err)
	}
	if !exists {
		return ErrWorkflowRunNotFound
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("pgstore: begin create workflow steps: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck // no-op after Commit

	query := `
		INSERT INTO workflow_steps (
			run_id, step_name, status, attempt, input
		) VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (run_id, step_name) DO NOTHING
	`
	for _, step := range steps {
		status := step.Status
		if status == "" {
			status = WorkflowStepStatusPending
		}
		if _, err := tx.Exec(ctx, query, runID, step.StepName, status, step.Attempt, step.Input); err != nil {
			return fmt.Errorf("pgstore: insert step %q: %w", step.StepName, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("pgstore: commit workflow steps: %w", err)
	}
	return nil
}

func (s *PgStore) GetWorkflowSteps(ctx context.Context, runID string) ([]*WorkflowStep, error) {
	query := fmt.Sprintf(`SELECT %s FROM workflow_steps WHERE run_id = $1 ORDER BY created_at ASC, step_name ASC`, workflowStepSelectCols)
	rows, err := s.pool.Query(ctx, query, runID)
	if err != nil {
		return nil, fmt.Errorf("pgstore: get workflow steps: %w", err)
	}
	defer rows.Close()

	var steps []*WorkflowStep
	for rows.Next() {
		step, err := scanWorkflowStepCols(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("pgstore: scan workflow step: %w", err)
		}
		steps = append(steps, step)
	}
	return steps, rows.Err()
}

func (s *PgStore) GetWorkflowStepAttempts(ctx context.Context, runID, stepName string) ([]*WorkflowStepAttempt, error) {
	var exists bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM workflow_runs WHERE id = $1)`, runID).Scan(&exists); err != nil {
		return nil, fmt.Errorf("pgstore: inspect workflow run for attempts: %w", err)
	}
	if !exists {
		return nil, ErrWorkflowRunNotFound
	}
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM workflow_steps WHERE run_id = $1 AND step_name = $2)`, runID, stepName).Scan(&exists); err != nil {
		return nil, fmt.Errorf("pgstore: inspect workflow step attempts: %w", err)
	}
	if !exists {
		return nil, ErrWorkflowStepNotFound
	}
	rows, err := s.pool.Query(ctx, `
		SELECT attempt, status, http_status, started_at, finished_at, next_attempt_at, error
		FROM workflow_step_attempts
		WHERE run_id = $1 AND step_name = $2
		ORDER BY attempt ASC
	`, runID, stepName)
	if err != nil {
		return nil, fmt.Errorf("pgstore: get workflow step attempts: %w", err)
	}
	defer rows.Close()
	attempts := make([]*WorkflowStepAttempt, 0)
	for rows.Next() {
		var attempt WorkflowStepAttempt
		attempt.RunID = runID
		attempt.StepName = stepName
		if err := rows.Scan(&attempt.Attempt, &attempt.Status, &attempt.HTTPStatus, &attempt.StartedAt, &attempt.FinishedAt, &attempt.NextAttemptAt, &attempt.Error); err != nil {
			return nil, fmt.Errorf("pgstore: scan workflow step attempt: %w", err)
		}
		attempts = append(attempts, &attempt)
	}
	return attempts, rows.Err()
}

// StartWorkflowStep persists the resolved input with the running transition.
// A retry therefore reads and reuses the same request body after recovery.
func (s *PgStore) StartWorkflowStep(ctx context.Context, runID, stepName string, attempt int, input json.RawMessage) (json.RawMessage, error) {
	if attempt < 1 {
		return nil, ErrWorkflowInvalidAttempt
	}
	if err := validateWorkflowJSON(input, true); err != nil {
		return nil, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("pgstore: begin start workflow step: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck // no-op after Commit
	var runStatus string
	if err := tx.QueryRow(ctx, `SELECT status FROM workflow_runs WHERE id = $1 FOR UPDATE`, runID).Scan(&runStatus); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrWorkflowRunNotFound
		}
		return nil, fmt.Errorf("pgstore: lock run to start workflow step: %w", err)
	}
	if runStatus == WorkflowRunStatusSucceeded || runStatus == WorkflowRunStatusFailed || runStatus == WorkflowRunStatusDead {
		return nil, fmt.Errorf("%w: workflow run is terminal", ErrConflict)
	}
	var storedInput []byte
	err = tx.QueryRow(ctx, `
		UPDATE workflow_steps
		SET status = 'running', attempt = $3, input = $4, error = NULL,
		    started_at = COALESCE(started_at, now()), next_retry_at = NULL,
		    finished_at = NULL
		WHERE run_id = $1 AND step_name = $2 AND status IN ('pending', 'awaiting_event')
		RETURNING input
	`, runID, stepName, attempt, input).Scan(&storedInput)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("pgstore: start workflow step: %w", err)
		}
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM workflow_steps WHERE run_id = $1 AND step_name = $2)`, runID, stepName).Scan(&exists); err != nil {
			return nil, fmt.Errorf("pgstore: inspect workflow step start conflict: %w", err)
		}
		if !exists {
			return nil, ErrWorkflowStepNotFound
		}
		return nil, fmt.Errorf("%w: workflow step is not pending or parked", ErrConflict)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO workflow_step_attempts (run_id, step_name, attempt, status)
		VALUES ($1, $2, $3, 'running')
		ON CONFLICT (run_id, step_name, attempt) DO UPDATE
		SET status = 'running', http_status = NULL, finished_at = NULL,
		    next_attempt_at = NULL, error = NULL
	`, runID, stepName, attempt); err != nil {
		return nil, fmt.Errorf("pgstore: start workflow step attempt: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE workflow_runs SET current_step = $2, updated_at = now() WHERE id = $1`, runID, stepName); err != nil {
		return nil, fmt.Errorf("pgstore: update workflow current step: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("pgstore: commit start workflow step: %w", err)
	}
	return json.RawMessage(storedInput), nil
}

func (s *PgStore) MarkWorkflowStepStatus(ctx context.Context, runID, stepName, status string, attempt int, output json.RawMessage, stepErr *string) error {
	return s.markWorkflowStepStatus(ctx, runID, stepName, status, attempt, nil, nil, output, stepErr)
}

// MarkWorkflowStepAttemptStatus atomically closes an executor attempt and
// updates its compact step summary.
func (s *PgStore) MarkWorkflowStepAttemptStatus(ctx context.Context, runID, stepName, status string, attempt int, httpStatus *int, output json.RawMessage, stepErr *string) error {
	if status != WorkflowStepStatusSucceeded && status != WorkflowStepStatusFailed && status != WorkflowStepStatusDead {
		return fmt.Errorf("%w: attempt completion requires a terminal status", ErrWorkflowInvalidStatus)
	}
	if err := validateWorkflowHTTPStatus(httpStatus); err != nil {
		return err
	}
	attemptStatus := WorkflowAttemptStatusFailed
	if status == WorkflowStepStatusSucceeded {
		attemptStatus = WorkflowAttemptStatusSucceeded
	}
	return s.markWorkflowStepStatus(ctx, runID, stepName, status, attempt, &attemptStatus, httpStatus, output, stepErr)
}

func (s *PgStore) markWorkflowStepStatus(ctx context.Context, runID, stepName, status string, attempt int, attemptStatus *string, httpStatus *int, output json.RawMessage, stepErr *string) error {
	if err := validateWorkflowStepStatus(status); err != nil {
		return err
	}
	if attempt < 0 {
		return ErrWorkflowInvalidAttempt
	}
	if err := validateWorkflowJSON(output, false); err != nil {
		return err
	}
	if err := validateWorkflowHTTPStatus(httpStatus); err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("pgstore: begin mark workflow step: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck // no-op after Commit
	// Match cancellation, callback completion, and wait parking lock order.
	var runIDLocked string
	if err := tx.QueryRow(ctx, `SELECT id FROM workflow_runs WHERE id = $1 FOR UPDATE`, runID).Scan(&runIDLocked); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrWorkflowRunNotFound
		}
		return fmt.Errorf("pgstore: lock workflow run for step status: %w", err)
	}

	query := `
		UPDATE workflow_steps
		SET status = $3,
		    attempt = $4,
		    output = COALESCE($5, output),
		    error = CASE WHEN $3 IN ('running', 'succeeded') THEN NULL ELSE COALESCE($6, error) END,
		    started_at = CASE WHEN $3 = 'running' AND started_at IS NULL THEN now() ELSE started_at END,
		    next_retry_at = CASE WHEN $3 = 'running' OR $3 IN ('succeeded', 'failed', 'dead', 'skipped') THEN NULL ELSE next_retry_at END,
		    finished_at = CASE WHEN $3 IN ('succeeded', 'failed', 'dead', 'skipped') THEN now() ELSE finished_at END
			WHERE run_id = $1 AND step_name = $2
			  AND (status NOT IN ('succeeded', 'failed', 'dead', 'skipped') OR status = $3)
	`
	tag, err := tx.Exec(ctx, query, runID, stepName, status, attempt, output, stepErr)
	if err != nil {
		return fmt.Errorf("pgstore: mark step status: %w", err)
	}
	if tag.RowsAffected() == 0 {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM workflow_steps WHERE run_id = $1 AND step_name = $2)`, runID, stepName).Scan(&exists); err != nil {
			return fmt.Errorf("pgstore: inspect workflow step transition conflict: %w", err)
		}
		if !exists {
			return ErrWorkflowStepNotFound
		}
		return fmt.Errorf("%w: workflow step is terminal", ErrConflict)
	}
	if attemptStatus != nil {
		tag, err = tx.Exec(ctx, `
			UPDATE workflow_step_attempts
			SET status = $4, http_status = $5, finished_at = clock_timestamp(),
			    next_attempt_at = NULL, error = $6
			WHERE run_id = $1 AND step_name = $2 AND attempt = $3
		`, runID, stepName, attempt, *attemptStatus, httpStatus, stepErr)
		if err != nil {
			return fmt.Errorf("pgstore: finish workflow step attempt: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return ErrWorkflowAttemptNotFound
		}
	}

	// Update current_step pointer and updated_at on workflow_runs
	runQuery := `
		UPDATE workflow_runs
		SET current_step = $2,
		    updated_at = now()
		WHERE id = $1
	`
	if _, err := tx.Exec(ctx, runQuery, runID, stepName); err != nil {
		return fmt.Errorf("pgstore: update run current step: %w", err)
	}

	return tx.Commit(ctx)
}

// ScheduleWorkflowStepRetry persists a retry deadline and scheduler wake in
// one transaction. Lock order matches cancellation and other step transitions.
func (s *PgStore) ScheduleWorkflowStepRetry(ctx context.Context, runID, stepName string, attempt int, retryAt time.Time, stepErr string) error {
	return s.scheduleWorkflowStepRetry(ctx, runID, stepName, attempt, retryAt, nil, stepErr, false)
}

// ScheduleWorkflowStepRetryWithHTTPStatus persists the retry and its failed
// executor attempt in the scheduler transaction.
func (s *PgStore) ScheduleWorkflowStepRetryWithHTTPStatus(ctx context.Context, runID, stepName string, attempt int, retryAt time.Time, httpStatus *int, stepErr string) error {
	if err := validateWorkflowHTTPStatus(httpStatus); err != nil {
		return err
	}
	return s.scheduleWorkflowStepRetry(ctx, runID, stepName, attempt, retryAt, httpStatus, stepErr, true)
}

func (s *PgStore) scheduleWorkflowStepRetry(ctx context.Context, runID, stepName string, attempt int, retryAt time.Time, httpStatus *int, stepErr string, recordAttempt bool) error {
	if attempt < 1 || retryAt.IsZero() || stepErr == "" {
		return ErrWorkflowInvalidRecord
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("pgstore: begin schedule workflow retry: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck // no-op after Commit
	var runStatus string
	var scheduledFor, now time.Time
	if err := tx.QueryRow(ctx, `SELECT status, scheduled_for, clock_timestamp() FROM workflow_runs WHERE id = $1 FOR UPDATE`, runID).Scan(&runStatus, &scheduledFor, &now); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrWorkflowRunNotFound
		}
		return fmt.Errorf("pgstore: lock workflow retry run: %w", err)
	}
	if runStatus == WorkflowRunStatusSucceeded || runStatus == WorkflowRunStatusFailed || runStatus == WorkflowRunStatusDead {
		return fmt.Errorf("%w: workflow run is terminal", ErrConflict)
	}
	wakeAt := earlierWorkflowWake(scheduledFor, retryAt.UTC(), now)
	tag, err := tx.Exec(ctx, `
		UPDATE workflow_steps
		SET status = 'pending', attempt = $3, next_retry_at = $4,
		    finished_at = NULL, error = $5
		WHERE run_id = $1 AND step_name = $2 AND status = 'running'
	`, runID, stepName, attempt, retryAt.UTC(), stepErr)
	if err != nil {
		return fmt.Errorf("pgstore: persist workflow retry step: %w", err)
	}
	if tag.RowsAffected() == 0 {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM workflow_steps WHERE run_id = $1 AND step_name = $2)`, runID, stepName).Scan(&exists); err != nil {
			return fmt.Errorf("pgstore: inspect workflow retry step: %w", err)
		}
		if !exists {
			return ErrWorkflowStepNotFound
		}
		return fmt.Errorf("%w: workflow step is not running", ErrConflict)
	}
	if recordAttempt {
		tag, err = tx.Exec(ctx, `
			UPDATE workflow_step_attempts
			SET status = 'retrying', http_status = $4, finished_at = $5,
			    next_attempt_at = $6, error = $7
			WHERE run_id = $1 AND step_name = $2 AND attempt = $3
		`, runID, stepName, attempt, httpStatus, now, retryAt.UTC(), stepErr)
		if err != nil {
			return fmt.Errorf("pgstore: finish retrying workflow attempt: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return ErrWorkflowAttemptNotFound
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE workflow_runs SET status = 'pending', current_step = $2, scheduled_for = $3, updated_at = now() WHERE id = $1`, runID, stepName, wakeAt); err != nil {
		return fmt.Errorf("pgstore: persist workflow retry wake: %w", err)
	}
	return tx.Commit(ctx)
}

// ParkWorkflowTimer commits the timer's activation and scheduler deadline in
// one transaction. Lock the run first, matching cancellation's lock order.
func (s *PgStore) ParkWorkflowTimer(ctx context.Context, runID, stepName string, duration time.Duration) (time.Time, error) {
	if duration <= 0 {
		return time.Time{}, ErrWorkflowInvalidRecord
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return time.Time{}, fmt.Errorf("pgstore: begin park workflow timer: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck // no-op after Commit
	var runStatus string
	var scheduledFor time.Time
	if err := tx.QueryRow(ctx, `SELECT status, scheduled_for FROM workflow_runs WHERE id = $1 FOR UPDATE`, runID).Scan(&runStatus, &scheduledFor); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return time.Time{}, ErrWorkflowRunNotFound
		}
		return time.Time{}, fmt.Errorf("pgstore: lock workflow timer run: %w", err)
	}
	if runStatus == WorkflowRunStatusSucceeded || runStatus == WorkflowRunStatusFailed || runStatus == WorkflowRunStatusDead {
		return time.Time{}, fmt.Errorf("%w: workflow run is terminal", ErrConflict)
	}
	var now time.Time
	if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return time.Time{}, fmt.Errorf("pgstore: read workflow timer clock: %w", err)
	}
	var startedAt time.Time
	err = tx.QueryRow(ctx, `
		UPDATE workflow_steps
		SET status = 'awaiting_event', started_at = COALESCE(started_at, clock_timestamp())
		WHERE run_id = $1 AND step_name = $2 AND status IN ('pending', 'awaiting_event')
		RETURNING started_at
	`, runID, stepName).Scan(&startedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return time.Time{}, ErrWorkflowStepNotFound
		}
		return time.Time{}, fmt.Errorf("pgstore: activate workflow timer: %w", err)
	}
	deadline := startedAt.Add(duration)
	deadline = earlierWorkflowWake(scheduledFor, deadline, now)
	if _, err := tx.Exec(ctx, `
		UPDATE workflow_runs
		SET status = 'awaiting_event', current_step = $2,
		    scheduled_for = $3, updated_at = now()
		WHERE id = $1
	`, runID, stepName, deadline); err != nil {
		return time.Time{}, fmt.Errorf("pgstore: schedule workflow timer: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return time.Time{}, fmt.Errorf("pgstore: commit workflow timer: %w", err)
	}
	return deadline, nil
}

func (s *PgStore) ParkWorkflowEvent(ctx context.Context, runID, stepName, eventName string, timeout time.Duration) (*WorkflowEvent, time.Time, error) {
	if eventName == "" || timeout <= 0 {
		return nil, time.Time{}, ErrWorkflowInvalidRecord
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("pgstore: begin park workflow event: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck // no-op after Commit
	var runStatus string
	var scheduledFor time.Time
	if err := tx.QueryRow(ctx, `SELECT status, scheduled_for FROM workflow_runs WHERE id = $1 FOR UPDATE`, runID).Scan(&runStatus, &scheduledFor); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, time.Time{}, ErrWorkflowRunNotFound
		}
		return nil, time.Time{}, fmt.Errorf("pgstore: lock workflow event run: %w", err)
	}
	if runStatus == WorkflowRunStatusSucceeded || runStatus == WorkflowRunStatusFailed || runStatus == WorkflowRunStatusDead {
		return nil, time.Time{}, fmt.Errorf("%w: workflow run is terminal", ErrConflict)
	}
	var stepStatus string
	var existingStartedAt *time.Time
	if err := tx.QueryRow(ctx, `SELECT status, started_at FROM workflow_steps WHERE run_id = $1 AND step_name = $2 FOR UPDATE`, runID, stepName).Scan(&stepStatus, &existingStartedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, time.Time{}, ErrWorkflowStepNotFound
		}
		return nil, time.Time{}, fmt.Errorf("pgstore: lock workflow event step: %w", err)
	}
	if stepStatus != WorkflowStepStatusPending && stepStatus != WorkflowStepStatusAwaitingEvent {
		return nil, time.Time{}, fmt.Errorf("%w: event wait step is not pending or awaiting", ErrConflict)
	}
	var matchDeadline *time.Time
	if existingStartedAt != nil {
		value := existingStartedAt.Add(timeout)
		matchDeadline = &value
	}
	query := fmt.Sprintf(`SELECT %s FROM workflow_events WHERE run_id = $1 AND event_name = $2 AND ($3::timestamptz IS NULL OR received_at < $3) ORDER BY received_at ASC, id ASC LIMIT 1`, workflowEventSelectCols)
	event, err := scanWorkflowEventCols(tx.QueryRow(ctx, query, runID, eventName, matchDeadline).Scan)
	if err == nil {
		if err := tx.Commit(ctx); err != nil {
			return nil, time.Time{}, fmt.Errorf("pgstore: commit matched workflow event: %w", err)
		}
		return event, time.Time{}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, time.Time{}, fmt.Errorf("pgstore: check matching workflow event: %w", err)
	}
	var now time.Time
	if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return nil, time.Time{}, fmt.Errorf("pgstore: read workflow event clock: %w", err)
	}
	var startedAt time.Time
	if err := tx.QueryRow(ctx, `
		UPDATE workflow_steps
		SET status = 'awaiting_event', started_at = COALESCE(started_at, clock_timestamp())
		WHERE run_id = $1 AND step_name = $2
		RETURNING started_at
	`, runID, stepName).Scan(&startedAt); err != nil {
		return nil, time.Time{}, fmt.Errorf("pgstore: activate workflow event wait: %w", err)
	}
	deadline := startedAt.Add(timeout)
	wakeAt := earlierWorkflowWake(scheduledFor, deadline, now)
	if _, err := tx.Exec(ctx, `
		UPDATE workflow_runs
		SET status = 'awaiting_event', current_step = $2,
		    scheduled_for = $3, updated_at = now()
		WHERE id = $1
	`, runID, stepName, wakeAt); err != nil {
		return nil, time.Time{}, fmt.Errorf("pgstore: schedule workflow event wait: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, time.Time{}, fmt.Errorf("pgstore: commit workflow event wait: %w", err)
	}
	return nil, deadline, nil
}

func (s *PgStore) ResolveWorkflowEventWait(ctx context.Context, runID, stepName, eventName string, timeout time.Duration, onTimeout bool) (*WorkflowEvent, bool, error) {
	if eventName == "" || timeout <= 0 {
		return nil, false, ErrWorkflowInvalidRecord
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("pgstore: begin resolve workflow event wait: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck // no-op after Commit
	var runStatus string
	if err := tx.QueryRow(ctx, `SELECT status FROM workflow_runs WHERE id = $1 FOR UPDATE`, runID).Scan(&runStatus); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, false, ErrWorkflowRunNotFound
		}
		return nil, false, fmt.Errorf("pgstore: lock workflow event wait run: %w", err)
	}
	if runStatus == WorkflowRunStatusSucceeded || runStatus == WorkflowRunStatusFailed || runStatus == WorkflowRunStatusDead {
		return nil, false, fmt.Errorf("%w: workflow run is terminal", ErrConflict)
	}
	var stepStatus string
	var startedAt *time.Time
	if err := tx.QueryRow(ctx, `SELECT status, started_at FROM workflow_steps WHERE run_id = $1 AND step_name = $2 FOR UPDATE`, runID, stepName).Scan(&stepStatus, &startedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, false, ErrWorkflowStepNotFound
		}
		return nil, false, fmt.Errorf("pgstore: lock workflow event wait step: %w", err)
	}
	if stepStatus != WorkflowStepStatusAwaitingEvent || startedAt == nil {
		return nil, false, fmt.Errorf("%w: workflow event wait is not active", ErrConflict)
	}
	query := fmt.Sprintf(`SELECT %s FROM workflow_events WHERE run_id = $1 AND event_name = $2 AND received_at < $3 ORDER BY received_at ASC, id ASC LIMIT 1`, workflowEventSelectCols)
	event, err := scanWorkflowEventCols(tx.QueryRow(ctx, query, runID, eventName, startedAt.Add(timeout)).Scan)
	if err == nil {
		if err := tx.Commit(ctx); err != nil {
			return nil, false, fmt.Errorf("pgstore: commit resolved workflow event: %w", err)
		}
		return event, false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, false, fmt.Errorf("pgstore: inspect workflow wait event: %w", err)
	}
	var now time.Time
	if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return nil, false, fmt.Errorf("pgstore: read workflow wait clock: %w", err)
	}
	if now.Before(startedAt.Add(timeout)) {
		if err := tx.Commit(ctx); err != nil {
			return nil, false, fmt.Errorf("pgstore: commit not-yet-expired workflow wait: %w", err)
		}
		return nil, false, nil
	}
	if onTimeout {
		if _, err := tx.Exec(ctx, `UPDATE workflow_steps SET status = 'succeeded', output = '{"timeout":true}'::jsonb, finished_at = $3 WHERE run_id = $1 AND step_name = $2`, runID, stepName, now); err != nil {
			return nil, false, fmt.Errorf("pgstore: complete timed-out workflow wait: %w", err)
		}
		if _, err := tx.Exec(ctx, `UPDATE workflow_runs SET status = 'pending', current_step = $2, scheduled_for = $3, updated_at = $3 WHERE id = $1`, runID, stepName, now); err != nil {
			return nil, false, fmt.Errorf("pgstore: schedule timeout handler: %w", err)
		}
	} else {
		message := "workflow event or callback wait timed out with no handler"
		if _, err := tx.Exec(ctx, `UPDATE workflow_steps SET status = 'dead', error = $3, finished_at = $4 WHERE run_id = $1 AND step_name = $2`, runID, stepName, message, now); err != nil {
			return nil, false, fmt.Errorf("pgstore: close timed-out workflow wait: %w", err)
		}
		if _, err := tx.Exec(ctx, `UPDATE workflow_runs SET status = 'dead', current_step = $2, last_error = $3, finished_at = $4, updated_at = $4 WHERE id = $1`, runID, stepName, message, now); err != nil {
			return nil, false, fmt.Errorf("pgstore: close timed-out workflow run: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, false, fmt.Errorf("pgstore: commit timed-out workflow wait: %w", err)
	}
	return nil, true, nil
}

func (s *PgStore) CompleteWorkflowCallback(ctx context.Context, runID, stepName, eventName, eventID string, timeout time.Duration, payload json.RawMessage) (bool, error) {
	if runID == "" || stepName == "" || eventName == "" || timeout <= 0 {
		return false, ErrWorkflowInvalidRecord
	}
	if _, err := uuid.Parse(eventID); err != nil {
		return false, ErrWorkflowInvalidRecord
	}
	if err := validateWorkflowJSON(payload, false); err != nil {
		return false, err
	}
	if len(payload) == 0 {
		payload = json.RawMessage("{}")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("pgstore: begin workflow callback: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck // no-op after Commit
	var runStatus string
	if err := tx.QueryRow(ctx, `SELECT status FROM workflow_runs WHERE id = $1 FOR UPDATE`, runID).Scan(&runStatus); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, ErrWorkflowRunNotFound
		}
		return false, fmt.Errorf("pgstore: lock workflow callback run: %w", err)
	}
	query := fmt.Sprintf(`SELECT %s FROM workflow_events WHERE id = $1`, workflowEventSelectCols)
	existing, err := scanWorkflowEventCols(tx.QueryRow(ctx, query, eventID).Scan)
	if err == nil {
		if existing.RunID != runID || existing.EventName != eventName || !equalWorkflowJSON(existing.Payload, payload) {
			return false, fmt.Errorf("%w: workflow callback payload differs", ErrConflict)
		}
		if err := tx.Commit(ctx); err != nil {
			return false, fmt.Errorf("pgstore: commit duplicate workflow callback: %w", err)
		}
		return true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return false, fmt.Errorf("pgstore: inspect workflow callback duplicate: %w", err)
	}
	if runStatus == WorkflowRunStatusSucceeded || runStatus == WorkflowRunStatusFailed || runStatus == WorkflowRunStatusDead {
		return false, ErrWorkflowCallbackClosed
	}
	var stepStatus string
	var startedAt *time.Time
	err = tx.QueryRow(ctx, `SELECT status, started_at FROM workflow_steps WHERE run_id = $1 AND step_name = $2 FOR UPDATE`, runID, stepName).Scan(&stepStatus, &startedAt)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return false, fmt.Errorf("pgstore: inspect workflow callback step: %w", err)
	}
	if err == nil && (stepStatus == WorkflowStepStatusSucceeded || stepStatus == WorkflowStepStatusFailed || stepStatus == WorkflowStepStatusDead || stepStatus == WorkflowStepStatusSkipped) {
		return false, ErrWorkflowCallbackClosed
	}
	var now time.Time
	if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return false, fmt.Errorf("pgstore: read workflow callback clock: %w", err)
	}
	if startedAt != nil && !now.Before(startedAt.Add(timeout)) {
		return false, ErrWorkflowCallbackExpired
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO workflow_events (id, run_id, event_name, payload, received_at)
		VALUES ($1, $2, $3, $4, $5)
	`, eventID, runID, eventName, payload, now); err != nil {
		return false, fmt.Errorf("pgstore: insert workflow callback event: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE workflow_runs
		SET status = 'pending', scheduled_for = $2, updated_at = $2
		WHERE id = $1 AND status = 'awaiting_event'
	`, runID, now); err != nil {
		return false, fmt.Errorf("pgstore: wake workflow callback run: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("pgstore: commit workflow callback: %w", err)
	}
	return false, nil
}

func (s *PgStore) InsertWorkflowEvent(ctx context.Context, e *WorkflowEvent) error {
	if e == nil {
		return fmt.Errorf("%w: nil event", ErrWorkflowInvalidRecord)
	}
	if e.RunID == "" || e.EventName == "" {
		return fmt.Errorf("%w: event run_id and event_name are required", ErrWorkflowInvalidRecord)
	}
	if err := validateWorkflowJSON(e.Payload, false); err != nil {
		return err
	}
	if e.ID == "" {
		e.ID = uuid.NewString()
	}
	if len(e.Payload) == 0 {
		e.Payload = json.RawMessage("{}")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("pgstore: begin workflow event: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck // no-op after Commit
	var runStatus string
	if err := tx.QueryRow(ctx, `SELECT status FROM workflow_runs WHERE id = $1 FOR UPDATE`, e.RunID).Scan(&runStatus); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrWorkflowRunNotFound
		}
		return fmt.Errorf("pgstore: lock workflow run for event: %w", err)
	}
	query := `
		INSERT INTO workflow_events (id, run_id, event_name, payload, received_at)
		VALUES ($1, $2, $3, $4, clock_timestamp())
		ON CONFLICT (id) DO UPDATE SET id = workflow_events.id
		WHERE workflow_events.run_id = excluded.run_id
		  AND workflow_events.event_name = excluded.event_name
		RETURNING received_at
	`
	err = tx.QueryRow(ctx, query, e.ID, e.RunID, e.EventName, e.Payload).Scan(&e.ReceivedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: workflow_events.id", ErrConflict)
		}
		return fmt.Errorf("pgstore: insert workflow event: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE workflow_runs
		SET status = 'pending', scheduled_for = now(), updated_at = now()
		WHERE id = $1 AND status = 'awaiting_event'
	`, e.RunID); err != nil {
		return fmt.Errorf("pgstore: wake workflow for event: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("pgstore: commit workflow event: %w", err)
	}
	return nil
}

func (s *PgStore) GetWorkflowEventsForRun(ctx context.Context, runID string) ([]*WorkflowEvent, error) {
	query := fmt.Sprintf(`SELECT %s FROM workflow_events WHERE run_id = $1 ORDER BY received_at ASC, id ASC`, workflowEventSelectCols)
	rows, err := s.pool.Query(ctx, query, runID)
	if err != nil {
		return nil, fmt.Errorf("pgstore: get workflow events: %w", err)
	}
	defer rows.Close()

	var events []*WorkflowEvent
	for rows.Next() {
		e, err := scanWorkflowEventCols(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("pgstore: scan workflow event: %w", err)
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

func (s *PgStore) FindMatchingEvent(ctx context.Context, runID, eventName string) (*WorkflowEvent, error) {
	query := fmt.Sprintf(`
		SELECT %s FROM workflow_events
		WHERE run_id = $1 AND event_name = $2
		ORDER BY received_at ASC, id ASC
		LIMIT 1
	`, workflowEventSelectCols)

	row := s.pool.QueryRow(ctx, query, runID, eventName)
	e, err := scanWorkflowEventCols(row.Scan)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrWorkflowEventNotFound
		}
		return nil, fmt.Errorf("pgstore: find matching event: %w", err)
	}
	return e, nil
}

func (s *PgStore) SweepExpiredWorkflowRuns(ctx context.Context, olderThan time.Duration) (int, error) {
	if olderThan < 0 {
		return 0, ErrWorkflowInvalidRecord
	}
	secs := int64(olderThan.Seconds())
	intervalStr := fmt.Sprintf("%d seconds", secs)

	query := `
		DELETE FROM workflow_runs
		WHERE finished_at IS NOT NULL
		  AND finished_at < now() - $1::interval
	`
	tag, err := s.pool.Exec(ctx, query, intervalStr)
	if err != nil {
		return 0, fmt.Errorf("pgstore: sweep expired workflow runs: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

func (s *PgStore) SweepExpiredWorkflowEvents(ctx context.Context, olderThan time.Duration) (int, error) {
	if olderThan < 0 {
		return 0, ErrWorkflowInvalidRecord
	}
	secs := int64(olderThan.Seconds())
	intervalStr := fmt.Sprintf("%d seconds", secs)

	query := `
		DELETE FROM workflow_events AS e
		USING workflow_runs AS r
		WHERE e.run_id = r.id
		  AND r.finished_at IS NOT NULL
		  AND e.received_at < now() - $1::interval
	`
	tag, err := s.pool.Exec(ctx, query, intervalStr)
	if err != nil {
		return 0, fmt.Errorf("pgstore: sweep expired workflow events: %w", err)
	}
	return int(tag.RowsAffected()), nil
}
