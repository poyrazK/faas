package state

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

const workflowRunSelectCols = `id, app_id, platform_tenant_id, workflow_name, status, current_step, input, output,
       definition_snapshot, scheduled_for, started_at, finished_at, last_error, created_at, updated_at, resume_count, cancelled_at, deployment_id`

const workflowStepSelectCols = `run_id, step_name, status, attempt, input, output,
       started_at, next_check_at, next_retry_at, finished_at, error, created_at, when_matched, when_evaluated_at, skip_reason, foreach_parent, foreach_index, foreach_count, retry_base`

const workflowEventSelectCols = `id, run_id, event_name, payload, received_at`

func scanWorkflowRunCols(scan func(...any) error) (*WorkflowRun, error) {
	var r WorkflowRun
	var inputBytes, outputBytes, defBytes []byte
	var runUUID string
	var platformTenantID, deploymentID pgtype.UUID
	if err := scan(&runUUID, &r.AppID, &platformTenantID, &r.WorkflowName, &r.Status, &r.CurrentStep,
		&inputBytes, &outputBytes, &defBytes, &r.ScheduledFor, &r.StartedAt,
		&r.FinishedAt, &r.LastError, &r.CreatedAt, &r.UpdatedAt, &r.ResumeCount, &r.CancelledAt, &deploymentID); err != nil {
		return nil, err
	}
	r.ID = runUUID
	r.DeploymentID = pgUUIDString(deploymentID)
	if platformTenantID.Valid {
		r.PlatformTenantID = uuid.UUID(platformTenantID.Bytes).String()
	}
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
		&s.Error, &s.CreatedAt, &s.WhenMatched, &s.WhenEvaluatedAt, &s.SkipReason, &s.ForEachParent, &s.ForEachIndex, &s.ForEachCount, &s.RetryBase); err != nil {
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

type workflowRunQueryer = sqlc.DBTX

func prepareWorkflowRun(r *WorkflowRun) error {
	if r == nil {
		return fmt.Errorf("%w: nil run", ErrWorkflowInvalidRecord)
	}
	if r.ResumeCount != 0 || r.CancelledAt != nil {
		return ErrWorkflowInvalidRecord
	}
	if r.ID == "" {
		r.ID = uuid.NewString()
	}
	if r.DeploymentID != "" {
		if id, err := uuid.Parse(r.DeploymentID); err != nil || id == uuid.Nil {
			return fmt.Errorf("%w: invalid deployment ID", ErrWorkflowInvalidRecord)
		}
	}
	if r.PlatformTenantID != "" {
		if _, err := uuid.Parse(r.PlatformTenantID); err != nil {
			return fmt.Errorf("%w: invalid platform tenant ID", ErrWorkflowInvalidRecord)
		}
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
	row, err := sqlc.New().InsertWorkflowRun(ctx, q, sqlc.InsertWorkflowRunParams{
		ID: mustPgUUID(r.ID), AppID: mustPgUUID(r.AppID), PlatformTenantID: mustPgUUID(r.PlatformTenantID),
		DeploymentID: mustPgUUID(r.DeploymentID), WorkflowName: r.WorkflowName, Status: r.Status,
		Input: r.Input, DefinitionSnapshot: r.DefinitionSnapshot, ScheduledFor: pgtype.Timestamptz{Time: r.ScheduledFor, Valid: true},
	})
	r.CreatedAt, r.UpdatedAt = timestamptzToTime(row.CreatedAt), timestamptzToTime(row.UpdatedAt)
	if err != nil {
		return fmt.Errorf("pgstore: create workflow run: %w", mapWorkflowCodePinError(err))
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
	if err := sqlc.New().LockWorkflowRunAdmission(ctx, tx, r.AppID); err != nil {
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

func (s *PgStore) GetWorkflowRunByIdempotencyKey(ctx context.Context, appID, workflowName, key string, requestFingerprint []byte) (*WorkflowRun, error) {
	if err := validateWorkflowRunCreateIdempotency(key, requestFingerprint); err != nil {
		return nil, err
	}
	var runID string
	var storedFingerprint []byte
	err := s.pool.QueryRow(ctx, `
		SELECT id::text, create_request_fingerprint
		FROM workflow_runs
		WHERE app_id = $1 AND workflow_name = $2 AND create_idempotency_key = $3
	`, appID, workflowName, key).Scan(&runID, &storedFingerprint)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrWorkflowRunNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("pgstore: look up workflow run idempotency key: %w", err)
	}
	if !bytes.Equal(storedFingerprint, requestFingerprint) {
		return nil, ErrWorkflowRunIdempotencyConflict
	}
	return s.GetWorkflowRun(ctx, runID)
}

func (s *PgStore) CreateWorkflowRunAdmittedWithIdempotencyKey(ctx context.Context, r *WorkflowRun, maxActive int, key string, requestFingerprint []byte) (int, bool, error) {
	if err := validateWorkflowRunCreateIdempotency(key, requestFingerprint); err != nil {
		return 0, false, err
	}
	if err := prepareWorkflowRun(r); err != nil {
		return 0, false, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, false, fmt.Errorf("pgstore: begin idempotent workflow admission: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck // no-op after Commit
	if err := sqlc.New().LockWorkflowRunAdmission(ctx, tx, r.AppID); err != nil {
		return 0, false, fmt.Errorf("pgstore: lock idempotent workflow admission: %w", err)
	}

	var existingID string
	var storedFingerprint []byte
	err = tx.QueryRow(ctx, `
		SELECT id::text, create_request_fingerprint
		FROM workflow_runs
		WHERE app_id = $1 AND workflow_name = $2 AND create_idempotency_key = $3
	`, r.AppID, r.WorkflowName, key).Scan(&existingID, &storedFingerprint)
	if err == nil {
		if !bytes.Equal(storedFingerprint, requestFingerprint) {
			return 0, false, ErrWorkflowRunIdempotencyConflict
		}
		query := fmt.Sprintf(`SELECT %s FROM workflow_runs WHERE id = $1`, workflowRunSelectCols)
		existing, err := scanWorkflowRunCols(tx.QueryRow(ctx, query, existingID).Scan)
		if err != nil {
			return 0, false, fmt.Errorf("pgstore: load idempotent workflow run: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return 0, false, fmt.Errorf("pgstore: commit idempotent workflow replay: %w", err)
		}
		*r = *existing
		return 0, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return 0, false, fmt.Errorf("pgstore: check workflow run idempotency key: %w", err)
	}
	if maxActive < 1 {
		return 0, false, ErrWorkflowRunQuotaExceeded
	}
	var active int
	if err := tx.QueryRow(ctx, `
		SELECT count(*) FROM workflow_runs
		WHERE app_id = $1 AND status IN ('pending', 'running', 'awaiting_event')
	`, r.AppID).Scan(&active); err != nil {
		return 0, false, fmt.Errorf("pgstore: count idempotent workflow admission: %w", err)
	}
	if active >= maxActive {
		return active, false, ErrWorkflowRunQuotaExceeded
	}
	if err := insertWorkflowRun(ctx, tx, r); err != nil {
		return active, false, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE workflow_runs
		SET create_idempotency_key = $2, create_request_fingerprint = $3
		WHERE id = $1
	`, r.ID, key, requestFingerprint); err != nil {
		return active, false, fmt.Errorf("pgstore: persist workflow run idempotency key: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return active, false, fmt.Errorf("pgstore: commit idempotent workflow admission: %w", err)
	}
	return active + 1, false, nil
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
	if opts.CreatedAfter != nil && opts.CreatedBefore != nil && opts.CreatedAfter.After(*opts.CreatedBefore) {
		return nil, 0, ErrWorkflowInvalidCreatedRange
	}
	if opts.Status != "" {
		if err := validateWorkflowRunStatus(opts.Status); err != nil {
			return nil, 0, err
		}
	}
	where := `app_id = $1`
	args := []any{appID}
	argIdx := 2
	if opts.PlatformTenantID != "" {
		where += fmt.Sprintf(` AND platform_tenant_id = $%d`, argIdx)
		args = append(args, opts.PlatformTenantID)
		argIdx++
	}
	if opts.Status != "" {
		where += fmt.Sprintf(` AND status = $%d`, argIdx)
		args = append(args, opts.Status)
		argIdx++
	}
	if opts.WorkflowName != "" {
		where += fmt.Sprintf(` AND workflow_name = $%d`, argIdx)
		args = append(args, opts.WorkflowName)
		argIdx++
	}
	if opts.CreatedAfter != nil {
		where += fmt.Sprintf(` AND created_at >= $%d`, argIdx)
		args = append(args, *opts.CreatedAfter)
		argIdx++
	}
	if opts.CreatedBefore != nil {
		where += fmt.Sprintf(` AND created_at <= $%d`, argIdx)
		args = append(args, *opts.CreatedBefore)
		argIdx++
	}

	countQuery := `SELECT count(*) FROM workflow_runs WHERE ` + where

	var total int
	if err := s.pool.QueryRow(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("pgstore: count workflow runs: %w", err)
	}

	query := fmt.Sprintf(`SELECT %s FROM workflow_runs WHERE %s`, workflowRunSelectCols, where)
	qArgs := append([]any(nil), args...)

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
	errorText := pgtype.Text{}
	if lastErr != nil {
		errorText = pgtype.Text{String: *lastErr, Valid: true}
	}
	rows, err := sqlc.New().MarkWorkflowRunStatusFenced(ctx, s.pool, sqlc.MarkWorkflowRunStatusFencedParams{RunID: mustPgUUID(id), Status: status, Output: output, LastError: errorText, Generation: workflowGenerationArg(ctx, id)})
	if err != nil {
		return fmt.Errorf("mark workflow run: %w", err)
	}
	return s.workflowRunWriteResult(ctx, id, rows, false)
}

func (s *PgStore) ClaimNextPendingRun(ctx context.Context) (*WorkflowRun, error) {
	query := fmt.Sprintf(`
		UPDATE workflow_runs
		SET status = 'running',
		    started_at = COALESCE(started_at, now()),
		    updated_at = now(), lease_until = now() + interval '5 minutes'
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

// ClaimNextDueWorkflowRun fairly selects an eligible app and tenant scope,
// reserving capacity and recording service order in the same transaction.
func (s *PgStore) ClaimNextDueWorkflowRun(ctx context.Context) (*WorkflowRun, error) {
	ctx, cancel := context.WithTimeout(ctx, api.WorkflowDispatchClaimTimeout)
	defer cancel()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("pgstore: begin workflow claim: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if err := q.LockWorkflowDispatchFairness(ctx, tx); err != nil {
		return nil, fmt.Errorf("pgstore: lock workflow dispatch: %w", err)
	}
	limits := sqlc.NextFairDueWorkflowRunParams{
		StaleMs:  int64(WorkflowRunStaleAfter / time.Millisecond),
		AppLimit: api.WorkflowDispatchMaxPerApp, TenantLimit: api.WorkflowDispatchMaxPerTenant,
	}
	candidate, err := q.NextFairDueWorkflowRun(ctx, tx, limits)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("pgstore: select fair workflow run: %w", err)
	}
	if err := q.LockWorkflowRunConcurrency(ctx, tx, sqlc.LockWorkflowRunConcurrencyParams{
		AppID: candidate.AppID, WorkflowName: candidate.WorkflowName,
	}); err != nil {
		return nil, fmt.Errorf("pgstore: lock workflow concurrency: %w", err)
	}
	available, err := q.WorkflowDispatchCapacityAvailable(ctx, tx, sqlc.WorkflowDispatchCapacityAvailableParams{
		ID: candidate.ID, StaleMs: limits.StaleMs, AppLimit: limits.AppLimit, TenantLimit: limits.TenantLimit,
	})
	if err != nil {
		return nil, fmt.Errorf("pgstore: recheck workflow capacity: %w", err)
	}
	if !available {
		return nil, ErrNotFound
	}
	row, err := q.ClaimDueLegacyWorkflowRun(ctx, tx, sqlc.ClaimDueLegacyWorkflowRunParams{ID: candidate.ID, StaleMs: limits.StaleMs})
	if err != nil {
		return nil, fmt.Errorf("pgstore: claim fair workflow run: %w", err)
	}
	run := workflowRunFromSQLC(row)
	if candidate.Status == WorkflowRunStatusRunning {
		if err := recoverWorkflowStepsTx(ctx, tx, run.ID); err != nil {
			return nil, fmt.Errorf("pgstore: recover stale workflow steps: %w", err)
		}
	}
	if err := q.RecordWorkflowDispatchClaim(ctx, tx, sqlc.RecordWorkflowDispatchClaimParams{
		AppID: candidate.AppID, PlatformTenantID: candidate.PlatformTenantID,
	}); err != nil {
		return nil, fmt.Errorf("pgstore: record workflow service order: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("pgstore: commit workflow claim: %w", err)
	}
	return run, nil
}

// ExtendWorkflowRunLease gives the active executor its declared timeout plus
// five minutes for cancellation and completion writes. Only a running run can
// be extended; a stale or terminal worker cannot revive it.
func (s *PgStore) ExtendWorkflowRunLease(ctx context.Context, runID string, timeout time.Duration) error {
	if timeout <= 0 {
		return ErrWorkflowInvalidInput
	}
	rows, err := sqlc.New().ExtendWorkflowRunLeaseFenced(ctx, s.pool, sqlc.ExtendWorkflowRunLeaseFencedParams{RunID: mustPgUUID(runID), TimeoutMs: int64(timeout / time.Millisecond), Generation: workflowGenerationArg(ctx, runID)})
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrWorkflowOutboundAttemptExpired
	}
	return nil
}

// ScheduleWorkflowRun updates the run's scheduler state and next due time.
func (s *PgStore) ScheduleWorkflowRun(ctx context.Context, id, status string, scheduledFor time.Time) error {
	if err := validateWorkflowRunStatus(status); err != nil {
		return err
	}
	rows, err := sqlc.New().ScheduleWorkflowRunFenced(ctx, s.pool, sqlc.ScheduleWorkflowRunFencedParams{RunID: mustPgUUID(id), Status: status, ScheduledFor: pgtype.Timestamptz{Time: scheduledFor.UTC(), Valid: true}, Generation: workflowGenerationArg(ctx, id)})
	if err != nil {
		return fmt.Errorf("schedule workflow run: %w", err)
	}
	return s.workflowRunWriteResult(ctx, id, rows, false)
}

func (s *PgStore) SetWorkflowRunWake(ctx context.Context, id, status string, scheduledFor time.Time) error {
	if status != WorkflowRunStatusPending && status != WorkflowRunStatusAwaitingEvent {
		return ErrWorkflowInvalidRecord
	}
	rows, err := sqlc.New().SetWorkflowRunWakeFenced(ctx, s.pool, sqlc.SetWorkflowRunWakeFencedParams{RunID: mustPgUUID(id), Status: status, ScheduledFor: pgtype.Timestamptz{Time: scheduledFor.UTC(), Valid: true}, Generation: workflowGenerationArg(ctx, id)})
	if err != nil {
		return fmt.Errorf("set workflow wake: %w", err)
	}
	return s.workflowRunWriteResult(ctx, id, rows, true)
}

// uncertain mutations become terminal unless provider idempotency is supported.
func (s *PgStore) RecoverWorkflowRun(ctx context.Context, id string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("pgstore: begin recover workflow: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck // no-op after Commit
	status, err := sqlc.New().LockWorkflowRecovery(ctx, tx, mustPgUUID(id))
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrWorkflowRunNotFound
	}
	if err != nil {
		return fmt.Errorf("pgstore: lock workflow recovery: %w", err)
	}
	if err := checkWorkflowGenerationTx(ctx, tx, id); err != nil {
		return err
	}

	if status == WorkflowRunStatusSucceeded || status == WorkflowRunStatusFailed || status == WorkflowRunStatusDead {
		return nil
	}
	if err := recoverWorkflowStepsTx(ctx, tx, id); err != nil {
		return fmt.Errorf("pgstore: recover workflow steps: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE workflow_runs
		SET status = 'pending', scheduled_for = now(), updated_at = now()
		WHERE id = $1
	`, id); err != nil {
		return fmt.Errorf("pgstore: recover workflow run: %w", err)
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
	run, err = s.cancelWorkflowRunTx(ctx, tx, id, reason)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("pgstore: commit workflow cancellation: %w", err)
	}
	return run, nil
}

func (s *PgStore) cancelWorkflowRunTx(ctx context.Context, tx pgx.Tx, id, reason string) (*WorkflowRun, error) {
	if _, err := tx.Exec(ctx, `
		UPDATE workflow_steps
		SET status = 'skipped', error = $2, finished_at = now(), next_retry_at = NULL
		WHERE run_id = $1 AND status IN ('pending', 'running', 'awaiting_event')
	`, id, reason); err != nil {
		return nil, fmt.Errorf("pgstore: skip workflow steps for cancellation: %w", err)
	}
	if err := sqlc.New().CancelWorkflowOutboundAttempts(ctx, tx, sqlc.CancelWorkflowOutboundAttemptsParams{RunID: mustPgUUID(id), Reason: reason}); err != nil {
		return nil, err
	}
	updateQuery := fmt.Sprintf(`
		UPDATE workflow_runs
		SET status = 'failed', last_error = $2, finished_at = now(), updated_at = now()
		WHERE id = $1
		RETURNING %s
	`, workflowRunSelectCols)
	run, err := scanWorkflowRunCols(tx.QueryRow(ctx, updateQuery, id, reason).Scan)
	if err != nil {
		return nil, fmt.Errorf("pgstore: mark workflow cancelled: %w", err)
	}
	if err := sqlc.New().RecordWorkflowCancellation(ctx, tx, mustPgUUID(id)); err != nil {
		return nil, fmt.Errorf("pgstore: record workflow cancellation: %w", err)
	}
	run.CancelledAt = run.FinishedAt
	return run, nil
}

// CancelUnstartedWorkflowRuns locks selected run rows in a stable order,
// rechecks that each is still pending and has never started, then commits all
// eligible cancellations together. Dispatcher claims use SKIP LOCKED on these
// same rows, so a claim that wins first makes the run ineligible here.
func (s *PgStore) CancelUnstartedWorkflowRuns(ctx context.Context, appID, workflowName string, ids []string, reason string) ([]WorkflowQueuedRunCancelResult, error) {
	appID, ids, err := normalizeWorkflowQueuedRunCancelSelection(appID, ids)
	if err != nil {
		return nil, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("pgstore: begin queued workflow cancellation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck // no-op after Commit

	lockOrder := append([]string(nil), ids...)
	sort.Strings(lockOrder)
	locked := make(map[string]*WorkflowRun, len(ids))
	selectQuery := fmt.Sprintf(`SELECT %s FROM workflow_runs WHERE id = $1 AND app_id = $2 FOR UPDATE`, workflowRunSelectCols)
	for _, id := range lockOrder {
		run, lockErr := scanWorkflowRunCols(tx.QueryRow(ctx, selectQuery, id, appID).Scan)
		if errors.Is(lockErr, pgx.ErrNoRows) {
			continue
		}
		if lockErr != nil {
			return nil, fmt.Errorf("pgstore: lock queued workflow run: %w", lockErr)
		}
		locked[id] = run
	}

	results := make([]WorkflowQueuedRunCancelResult, 0, len(ids))
	for _, id := range ids {
		run := locked[id]
		if run == nil {
			results = append(results, workflowQueuedRunCancelResult(id, nil, WorkflowQueuedRunCancelNotFound))
			continue
		}
		outcome := ClassifyWorkflowRunForQueuedCancel(run, workflowName)
		if outcome == WorkflowQueuedRunCancelEligible {
			run, err = s.cancelWorkflowRunTx(ctx, tx, id, reason)
			if err != nil {
				return nil, err
			}
			outcome = WorkflowQueuedRunCancelCancelled
		}
		results = append(results, workflowQueuedRunCancelResult(id, run, outcome))
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("pgstore: commit queued workflow cancellation: %w", err)
	}
	return results, nil
}

// RetryWorkflowStep requeues one failed HTTP step in the existing workflow
// run. The run and step rows are locked before eligibility is checked, then
// the per-app admission lock serializes the active-run quota transition.
func (s *PgStore) RetryWorkflowStep(ctx context.Context, runID, stepName string, maxActive int) (*WorkflowRun, int, error) {
	if maxActive < 1 {
		return nil, 0, ErrWorkflowRunQuotaExceeded
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("pgstore: begin workflow step retry: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck // no-op after Commit
	queries := sqlc.New()
	lockedRun, err := queries.LockWorkflowRunForManualRetry(ctx, tx, runID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, 0, ErrWorkflowRunNotFound
	}
	if err != nil {
		return nil, 0, fmt.Errorf("pgstore: lock workflow run for retry: %w", err)
	}
	lockedSteps, err := queries.LockWorkflowStepsForManualRetry(ctx, tx, runID)
	if err != nil {
		return nil, 0, fmt.Errorf("pgstore: lock workflow steps for retry: %w", err)
	}
	stepViews := make([]*WorkflowStep, 0, len(lockedSteps))
	for _, row := range lockedSteps {
		stepViews = append(stepViews, &WorkflowStep{StepName: row.StepName, Status: row.Status, Attempt: int(row.Attempt), Error: workflowPGTextPtr(row.Error)})
	}
	runView := workflowRunFromSQLC(lockedRun)
	reopen, err := workflowRetryPlan(runView, stepName, stepViews)
	if err != nil {
		return nil, 0, err
	}
	appID := pgUUIDString(lockedRun.AppID)
	if err := queries.LockWorkflowRetryAdmission(ctx, tx, appID); err != nil {
		return nil, 0, fmt.Errorf("pgstore: lock workflow retry quota: %w", err)
	}
	activeCount, err := queries.CountActiveWorkflowRunsForRetry(ctx, tx, appID)
	if err != nil {
		return nil, 0, fmt.Errorf("pgstore: count active workflow runs for retry: %w", err)
	}
	active := int(activeCount)
	if active >= maxActive {
		return nil, active, ErrWorkflowRunQuotaExceeded
	}
	changed, err := queries.RequeueFailedWorkflowStepForRetry(ctx, tx, sqlc.RequeueFailedWorkflowStepForRetryParams{RunID: runID, StepName: stepName})
	if err != nil || changed != 1 {
		if err != nil {
			return nil, active, fmt.Errorf("pgstore: requeue failed workflow step: %w", err)
		}
		return nil, active, ErrWorkflowRetryNotAllowed
	}
	changed, err = queries.ReopenSkippedWorkflowStepsForRetry(ctx, tx, sqlc.ReopenSkippedWorkflowStepsForRetryParams{RunID: runID, StepNames: reopen})
	if err != nil || changed != int64(len(reopen)) {
		if err != nil {
			return nil, active, fmt.Errorf("pgstore: reopen skipped workflow steps: %w", err)
		}
		return nil, active, ErrWorkflowRetryNotAllowed
	}
	updated, err := queries.RequeueWorkflowRunForRetry(ctx, tx, sqlc.RequeueWorkflowRunForRetryParams{RunID: runID, StepName: stepName})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, active, ErrWorkflowRetryNotAllowed
	}
	if err != nil {
		return nil, active, fmt.Errorf("pgstore: requeue workflow run: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, active, fmt.Errorf("pgstore: commit workflow step retry: %w", err)
	}
	return workflowRunFromSQLC(updated), active + 1, nil
}

func workflowRunFromSQLC(row sqlc.WorkflowRun) *WorkflowRun {
	return &WorkflowRun{
		DeploymentID: pgUUIDString(row.DeploymentID), ResumeCount: int(row.ResumeCount), CancelledAt: timestamptzToTimePtr(row.CancelledAt),
		ID: pgUUIDString(row.ID), AppID: pgUUIDString(row.AppID), PlatformTenantID: pgUUIDString(row.PlatformTenantID), WorkflowName: row.WorkflowName,
		Status: row.Status, CurrentStep: workflowPGTextPtr(row.CurrentStep),
		Input: cloneWorkflowJSON(row.Input), Output: cloneWorkflowJSON(row.Output),
		DefinitionSnapshot: cloneWorkflowJSON(row.DefinitionSnapshot),
		ScheduledFor:       timestamptzToTime(row.ScheduledFor), StartedAt: timestamptzToTimePtr(row.StartedAt),
		FinishedAt: timestamptzToTimePtr(row.FinishedAt), LastError: workflowPGTextPtr(row.LastError),
		CreatedAt: timestamptzToTime(row.CreatedAt), UpdatedAt: timestamptzToTime(row.UpdatedAt),
	}
}

func workflowPGTextPtr(value pgtype.Text) *string {
	if !value.Valid {
		return nil
	}
	text := value.String
	return &text
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
		if step.RetryBase != 0 {
			return ErrWorkflowInvalidRecord
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
			run_id, step_name, status, attempt, input,
			foreach_parent, foreach_index, foreach_count,
			when_matched, when_evaluated_at, skip_reason
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		ON CONFLICT (run_id, step_name) DO NOTHING
	`
	for _, step := range steps {
		status := step.Status
		if status == "" {
			status = WorkflowStepStatusPending
		}
		if _, err := tx.Exec(ctx, query, runID, step.StepName, status, step.Attempt, step.Input,
			step.ForEachParent, step.ForEachIndex, step.ForEachCount,
			step.WhenMatched, step.WhenEvaluatedAt, step.SkipReason); err != nil {
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
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	if len(attempts) == 0 {
		return attempts, nil
	}
	effects, err := sqlc.New().ListWorkflowOperationEffects(ctx, s.pool, sqlc.ListWorkflowOperationEffectsParams{RunID: runID, StepName: stepName})
	if err != nil {
		return nil, fmt.Errorf("pgstore: get workflow operation effects: %w", err)
	}
	byAttempt := make(map[int]*WorkflowStepAttempt, len(attempts))
	for _, attempt := range attempts {
		byAttempt[attempt.Attempt] = attempt
	}
	for _, effect := range effects {
		attempt := byAttempt[int(effect.Generation)]
		if attempt == nil {
			continue
		}
		attempt.Effects = append(attempt.Effects, api.OperationEffectRecord{
			ID: effect.ID, Name: effect.Name, Generation: effect.Generation,
			WebhookID: effect.WebhookID, DeliveryID: effect.DeliveryID, Type: effect.Type,
			Status: effect.Status, Attempt: int(effect.Attempt), LastError: effect.LastError,
		})
	}
	return attempts, nil
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
	var runStatus, appID, workflowName string
	var definitionSnapshot []byte
	if err := tx.QueryRow(ctx, `SELECT status, definition_snapshot, app_id::text, workflow_name FROM workflow_runs WHERE id = $1 FOR UPDATE`, runID).Scan(&runStatus, &definitionSnapshot, &appID, &workflowName); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrWorkflowRunNotFound
		}
		return nil, fmt.Errorf("pgstore: lock run to start workflow step: %w", err)
	}
	if err := checkWorkflowGenerationTx(ctx, tx, runID); err != nil {
		return nil, err
	}

	current, err := sqlc.New().WorkflowOutboundStartCurrent(ctx, tx, sqlc.WorkflowOutboundStartCurrentParams{RunID: mustPgUUID(runID), StepName: stepName, Attempt: int32(attempt)})
	if err != nil {
		return nil, err
	}
	if !current {
		var stepStatus string
		var foreachParent pgtype.Text
		if err := tx.QueryRow(ctx, `SELECT status, foreach_parent FROM workflow_steps WHERE run_id = $1 AND step_name = $2`, runID, stepName).Scan(&stepStatus, &foreachParent); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, ErrWorkflowStepNotFound
			}
			return nil, fmt.Errorf("pgstore: inspect stale workflow step start: %w", err)
		}
		if foreachParent.Valid && stepStatus == WorkflowStepStatusSkipped {
			return nil, ErrWorkflowGuardNotReady
		}
		return nil, ErrWorkflowOutboundAttemptExpired
	}
	if runStatus == WorkflowRunStatusSucceeded || runStatus == WorkflowRunStatusFailed || runStatus == WorkflowRunStatusDead {
		return nil, fmt.Errorf("%w: workflow run is terminal", ErrConflict)
	}
	allowed, err := sqlc.New().WorkflowGuardStartAllowed(ctx, tx, sqlc.WorkflowGuardStartAllowedParams{RunID: mustPgUUID(runID), StepName: stepName})
	if err != nil {
		return nil, err
	}
	if !allowed {
		return nil, ErrWorkflowGuardNotReady
	}
	parallelLimit := 1
	if parent, _, isItem := api.WorkflowForEachItemIdentity(stepName); isItem {
		parallelLimit = workflowForEachMaxParallel(definitionSnapshot, parent)
	}
	foreachAllowed, err := sqlc.New().WorkflowForEachStartAllowed(ctx, tx, sqlc.WorkflowForEachStartAllowedParams{RunID: mustPgUUID(runID), StepName: stepName, Input: input, ParallelLimit: int32(parallelLimit)})
	if err != nil {
		return nil, err
	}
	if !foreachAllowed {
		return nil, ErrWorkflowGuardNotReady
	}
	if actionLimit := workflowRunMaxConcurrentActions(definitionSnapshot); actionLimit > 0 {
		var stepStatus string
		if err := tx.QueryRow(ctx, `SELECT status FROM workflow_steps WHERE run_id = $1 AND step_name = $2 FOR UPDATE`, runID, stepName).Scan(&stepStatus); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, ErrWorkflowStepNotFound
			}
			return nil, fmt.Errorf("pgstore: read workflow step for action admission: %w", err)
		}
		if stepStatus != WorkflowStepStatusPending && stepStatus != WorkflowStepStatusAwaitingEvent {
			return nil, fmt.Errorf("%w: workflow step is not pending or parked", ErrConflict)
		}
		q := sqlc.New()
		if err := q.LockWorkflowActionAdmission(ctx, tx, appID+"|"+workflowName); err != nil {
			return nil, fmt.Errorf("pgstore: lock workflow action admission: %w", err)
		}
		activeActions, err := q.CountWorkflowRunningActionsForAdmission(ctx, tx, sqlc.CountWorkflowRunningActionsForAdmissionParams{AppID: mustPgUUID(appID), WorkflowName: workflowName})
		if err != nil {
			return nil, fmt.Errorf("pgstore: count workflow action admission: %w", err)
		}
		if activeActions >= int64(actionLimit) {
			return nil, ErrWorkflowActionConcurrencyLimit
		}
	}
	var storedInput []byte
	err = tx.QueryRow(ctx, `
		UPDATE workflow_steps
		SET status = 'running', attempt = $3, input = $4, error = NULL,
		    outbound_attempt_token = gen_random_uuid(),
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
	if err := checkWorkflowGenerationTx(ctx, tx, runID); err != nil {
		return err
	}

	if attemptStatus != nil {
		if err := checkWorkflowOutboundCompletionTx(ctx, tx, runID, stepName, attempt); err != nil {
			return err
		}
	}

	if status == WorkflowStepStatusSucceeded && attemptStatus != nil {
		steps, readErr := workflowControlSteps(ctx, tx, runID)
		if readErr != nil {
			return readErr
		}
		if child := steps[stepName]; child.ForEachParent != nil {
			parent := steps[*child.ForEachParent]
			if parent.ForEachCount == nil {
				return ErrWorkflowGuardNotReady
			}
			var definitionSnapshot []byte
			if err := tx.QueryRow(ctx, `SELECT definition_snapshot FROM workflow_runs WHERE id = $1`, runID).Scan(&definitionSnapshot); err != nil {
				return fmt.Errorf("pgstore: read workflow definition for for_each output: %w", err)
			}
			continueOnFailure := workflowForEachContinuesOnFailure(definitionSnapshot, parent.StepName)
			if _, limitErr := workflowForEachOutputs(steps, parent.StepName, *parent.ForEachCount, stepName, output, continueOnFailure); limitErr != nil {
				return limitErr
			}
		}
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
	if err := checkWorkflowGenerationTx(ctx, tx, runID); err != nil {
		return err
	}

	if recordAttempt {
		if err := checkWorkflowOutboundCompletionTx(ctx, tx, runID, stepName, attempt); err != nil {
			return err
		}
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
	if err := checkWorkflowGenerationTx(ctx, tx, runID); err != nil {
		return time.Time{}, err
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
	if err := checkWorkflowGenerationTx(ctx, tx, runID); err != nil {
		return nil, time.Time{}, err
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
	if err := checkWorkflowGenerationTx(ctx, tx, runID); err != nil {
		return nil, false, err
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

func lockTenantWorkflowContinuationRun(ctx context.Context, tx pgx.Tx, tenantID, runID string) (string, error) {
	if _, err := uuid.Parse(tenantID); err != nil {
		return "", ErrWorkflowRunNotFound
	}
	if _, err := uuid.Parse(runID); err != nil {
		return "", ErrWorkflowRunNotFound
	}
	var status string
	var accountID, appID string
	err := tx.QueryRow(ctx, `
		SELECT r.status, a.account_id, a.id
		FROM workflow_runs r
		JOIN apps a ON a.id = r.app_id
		JOIN platform_tenants t ON t.id = r.platform_tenant_id
		  AND t.account_id = a.account_id AND t.status = 'active'
		WHERE r.id = $1 AND r.platform_tenant_id = $2
		FOR UPDATE OF r, t
	`, runID, tenantID).Scan(&status, &accountID, &appID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrWorkflowRunNotFound
	}
	if err != nil {
		return "", fmt.Errorf("pgstore: authorize tenant workflow continuation: %w", err)
	}
	var hasActiveLink bool
	rows, err := tx.Query(ctx, `
		SELECT c.id FROM api_consumers c
		WHERE c.account_id = $1 AND c.app_id = $2 AND c.platform_tenant_id = $3
		  AND c.status = 'active' AND c.revoked_at IS NULL
		FOR UPDATE
	`, accountID, appID, tenantID)
	if err != nil {
		return "", fmt.Errorf("pgstore: lock tenant workflow API consumer link: %w", err)
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return "", fmt.Errorf("pgstore: scan tenant workflow API consumer link: %w", err)
		}
		hasActiveLink = true
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return "", fmt.Errorf("pgstore: read tenant workflow API consumer links: %w", err)
	}
	rows.Close()
	rows, err = tx.Query(ctx, `
		SELECT ts.id FROM tenant_surfaces ts
		WHERE ts.account_id = $1 AND ts.app_id = $2 AND ts.platform_tenant_id = $3
		  AND ts.status = 'active'
		FOR UPDATE
	`, accountID, appID, tenantID)
	if err != nil {
		return "", fmt.Errorf("pgstore: lock tenant workflow surface link: %w", err)
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return "", fmt.Errorf("pgstore: scan tenant workflow surface link: %w", err)
		}
		hasActiveLink = true
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return "", fmt.Errorf("pgstore: read tenant workflow surface links: %w", err)
	}
	rows.Close()
	if !hasActiveLink {
		return "", ErrWorkflowRunNotFound
	}
	return status, nil
}

func (s *PgStore) CompleteWorkflowCallback(ctx context.Context, runID, stepName, eventName, eventID string, timeout time.Duration, payload json.RawMessage) (bool, error) {
	return s.completeWorkflowCallback(ctx, "", runID, stepName, eventName, eventID, timeout, payload)
}

func (s *PgStore) CompleteTenantWorkflowCallback(ctx context.Context, tenantID, runID, stepName, eventName, eventID string, timeout time.Duration, payload json.RawMessage) (bool, error) {
	if tenantID == "" {
		return false, ErrWorkflowInvalidRecord
	}
	return s.completeWorkflowCallback(ctx, tenantID, runID, stepName, eventName, eventID, timeout, payload)
}

func (s *PgStore) completeWorkflowCallback(ctx context.Context, tenantID, runID, stepName, eventName, eventID string, timeout time.Duration, payload json.RawMessage) (bool, error) {
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
	if tenantID == "" {
		if err := tx.QueryRow(ctx, `SELECT status FROM workflow_runs WHERE id = $1 FOR UPDATE`, runID).Scan(&runStatus); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return false, ErrWorkflowRunNotFound
			}
			return false, fmt.Errorf("pgstore: lock workflow callback run: %w", err)
		}
	} else {
		var err error
		runStatus, err = lockTenantWorkflowContinuationRun(ctx, tx, tenantID, runID)
		if err != nil {
			return false, err
		}
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
	return s.insertWorkflowEvent(ctx, "", e)
}

func (s *PgStore) InsertTenantWorkflowEvent(ctx context.Context, tenantID string, e *WorkflowEvent) error {
	if tenantID == "" {
		return ErrWorkflowInvalidRecord
	}
	return s.insertWorkflowEvent(ctx, tenantID, e)
}

func (s *PgStore) insertWorkflowEvent(ctx context.Context, tenantID string, e *WorkflowEvent) error {
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
	if tenantID == "" {
		if err := tx.QueryRow(ctx, `SELECT status FROM workflow_runs WHERE id = $1 FOR UPDATE`, e.RunID).Scan(&runStatus); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrWorkflowRunNotFound
			}
			return fmt.Errorf("pgstore: lock workflow run for event: %w", err)
		}
	} else {
		var err error
		runStatus, err = lockTenantWorkflowContinuationRun(ctx, tx, tenantID, e.RunID)
		if err != nil {
			return err
		}
		if runStatus != WorkflowRunStatusPending && runStatus != WorkflowRunStatusRunning && runStatus != WorkflowRunStatusAwaitingEvent {
			return ErrWorkflowNotRunning
		}
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

func (s *PgStore) workflowRunWriteResult(ctx context.Context, id string, rows int64, noop bool) error {
	if rows > 0 {
		return nil
	}
	run, err := s.GetWorkflowRun(ctx, id)
	if err != nil {
		return err
	}
	if !WorkflowRunGenerationMatches(ctx, id, run.ResumeCount) {
		return ErrWorkflowOutboundAttemptExpired
	}
	if noop {
		return nil
	}
	return fmt.Errorf("%w: workflow run is terminal", ErrConflict)
}
