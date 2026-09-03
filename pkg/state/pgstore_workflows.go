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
       started_at, finished_at, error, created_at`

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
		&inputBytes, &outputBytes, &s.StartedAt, &s.FinishedAt,
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
	if r.ID == "" {
		r.ID = uuid.NewString()
	}
	if r.Status == "" {
		r.Status = WorkflowRunStatusPending
	}
	if r.ScheduledFor.IsZero() {
		r.ScheduledFor = time.Now().UTC()
	}
	if len(r.Input) == 0 {
		r.Input = json.RawMessage("{}")
	}

	query := `
		INSERT INTO workflow_runs (
			id, app_id, workflow_name, status, input, definition_snapshot, scheduled_for
		) VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING created_at, updated_at
	`
	err := s.pool.QueryRow(ctx, query,
		r.ID, r.AppID, r.WorkflowName, r.Status, r.Input, r.DefinitionSnapshot, r.ScheduledFor,
	).Scan(&r.CreatedAt, &r.UpdatedAt)
	if err != nil {
		return fmt.Errorf("pgstore: create workflow run: %w", err)
	}
	return nil
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

	query += ` ORDER BY created_at DESC`
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
	query := `
		UPDATE workflow_runs
		SET status = $2,
		    output = COALESCE($3, output),
		    last_error = COALESCE($4, last_error),
		    started_at = CASE WHEN $2 = 'running' AND started_at IS NULL THEN now() ELSE started_at END,
		    finished_at = CASE WHEN $2 IN ('succeeded', 'failed', 'dead') THEN now() ELSE finished_at END,
		    updated_at = now()
		WHERE id = $1
	`
	tag, err := s.pool.Exec(ctx, query, id, status, output, lastErr)
	if err != nil {
		return fmt.Errorf("pgstore: mark workflow run status: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrWorkflowRunNotFound
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
			ORDER BY scheduled_for ASC
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
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("pgstore: begin create workflow steps: %w", err)
	}
	defer tx.Rollback(ctx)

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
	query := fmt.Sprintf(`SELECT %s FROM workflow_steps WHERE run_id = $1 ORDER BY created_at ASC`, workflowStepSelectCols)
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

func (s *PgStore) MarkWorkflowStepStatus(ctx context.Context, runID, stepName, status string, attempt int, output json.RawMessage, stepErr *string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("pgstore: begin mark workflow step: %w", err)
	}
	defer tx.Rollback(ctx)

	query := `
		UPDATE workflow_steps
		SET status = $3,
		    attempt = $4,
		    output = COALESCE($5, output),
		    error = COALESCE($6, error),
		    started_at = CASE WHEN $3 = 'running' AND started_at IS NULL THEN now() ELSE started_at END,
		    finished_at = CASE WHEN $3 IN ('succeeded', 'failed', 'dead', 'skipped') THEN now() ELSE finished_at END
		WHERE run_id = $1 AND step_name = $2
	`
	tag, err := tx.Exec(ctx, query, runID, stepName, status, attempt, output, stepErr)
	if err != nil {
		return fmt.Errorf("pgstore: mark step status: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrWorkflowStepNotFound
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

func (s *PgStore) InsertWorkflowEvent(ctx context.Context, e *WorkflowEvent) error {
	if e.ID == "" {
		e.ID = uuid.NewString()
	}
	if len(e.Payload) == 0 {
		e.Payload = json.RawMessage("{}")
	}

	query := `
		INSERT INTO workflow_events (id, run_id, event_name, payload)
		VALUES ($1, $2, $3, $4)
		RETURNING received_at
	`
	err := s.pool.QueryRow(ctx, query, e.ID, e.RunID, e.EventName, e.Payload).Scan(&e.ReceivedAt)
	if err != nil {
		return fmt.Errorf("pgstore: insert workflow event: %w", err)
	}
	return nil
}

func (s *PgStore) GetWorkflowEventsForRun(ctx context.Context, runID string) ([]*WorkflowEvent, error) {
	query := fmt.Sprintf(`SELECT %s FROM workflow_events WHERE run_id = $1 ORDER BY received_at ASC`, workflowEventSelectCols)
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
		ORDER BY received_at ASC
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
	secs := int64(olderThan.Seconds())
	intervalStr := fmt.Sprintf("%d seconds", secs)

	query := `
		DELETE FROM workflow_events
		WHERE received_at < now() - $1::interval
	`
	tag, err := s.pool.Exec(ctx, query, intervalStr)
	if err != nil {
		return 0, fmt.Errorf("pgstore: sweep expired workflow events: %w", err)
	}
	return int(tag.RowsAffected()), nil
}
