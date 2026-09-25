package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/db"
)

var _ AppTaskStore = (*PgStore)(nil)

func nullableCronID(id string) any {
	if id == "" {
		return nil
	}
	return id
}

const appTaskSelectColumns = `id, account_id, app_id, deployment_id, kind,
       command, command_shell, deployment_scope, artifact_key, image_digest,
       status, timeout_seconds, max_output_bytes, retry_max, retry_backoff_seconds,
       attempt_count, retry_at,
       lease_token, lease_owner, lease_expires_at, cancel_requested_at,
       stdout_tail, stderr_tail, output_truncated, exit_code,
       failure_code, failure_message, started_at, finished_at,
       created_at, updated_at, cron_id, scheduled_for`

type appTaskRowScanner interface {
	Scan(dest ...any) error
}

func scanAppTask(row appTaskRowScanner) (AppTask, error) {
	var task AppTask
	var id, accountID, appID, deploymentID pgtype.UUID
	var cronID pgtype.UUID
	var leaseToken pgtype.UUID
	var leaseOwner, failureCode, failureMessage pgtype.Text
	var leaseExpiresAt, cancelRequestedAt, startedAt, finishedAt, scheduledFor, retryAt pgtype.Timestamptz
	var exitCode pgtype.Int4
	if err := row.Scan(
		&id, &accountID, &appID, &deploymentID, &task.Kind,
		&task.Command, &task.CommandShell, &task.DeploymentScope, &task.ArtifactKey, &task.ImageDigest,
		&task.Status, &task.TimeoutSeconds, &task.MaxOutputBytes,
		&task.RetryMax, &task.RetryBackoffSeconds, &task.AttemptCount, &retryAt,
		&leaseToken, &leaseOwner, &leaseExpiresAt, &cancelRequestedAt,
		&task.StdoutTail, &task.StderrTail, &task.OutputTruncated, &exitCode,
		&failureCode, &failureMessage, &startedAt, &finishedAt,
		&task.CreatedAt, &task.UpdatedAt, &cronID, &scheduledFor,
	); err != nil {
		return AppTask{}, err
	}
	task.ID = pgUUIDString(id)
	task.AccountID = pgUUIDString(accountID)
	task.AppID = pgUUIDString(appID)
	task.DeploymentID = pgUUIDString(deploymentID)
	if cronID.Valid {
		task.CronID = pgUUIDString(cronID)
	}
	task.ScheduledFor = timestamptzToTimePtr(scheduledFor)
	task.RetryAt = timestamptzToTimePtr(retryAt)
	task.LeaseToken = executionUUIDPtr(leaseToken)
	task.LeaseOwner = executionStringPtr(leaseOwner)
	task.LeaseExpiresAt = timestamptzToTimePtr(leaseExpiresAt)
	task.CancelRequested = timestamptzToTimePtr(cancelRequestedAt)
	task.ExitCode = executionIntPtr(exitCode)
	task.FailureCode = executionStringPtr(failureCode)
	task.FailureMessage = executionStringPtr(failureMessage)
	task.StartedAt = timestamptzToTimePtr(startedAt)
	task.FinishedAt = timestamptzToTimePtr(finishedAt)
	task.CreatedAt = task.CreatedAt.UTC()
	task.UpdatedAt = task.UpdatedAt.UTC()
	return task, nil
}

func scanAppTaskRows(rows pgx.Rows) ([]AppTask, error) {
	out := make([]AppTask, 0)
	for rows.Next() {
		task, err := scanAppTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, task)
	}
	return out, rows.Err()
}

func (s *PgStore) CreateAppTask(ctx context.Context, params CreateAppTaskParams) (AppTask, error) {
	resolved, err := resolveCreateAppTask(params)
	if err != nil {
		return AppTask{}, err
	}
	row := s.pool.QueryRow(ctx, `
		insert into app_tasks (
			account_id, app_id, deployment_id, kind, command, command_shell,
			deployment_scope, artifact_key, image_digest, timeout_seconds,
			max_output_bytes, created_at, updated_at, cron_id, scheduled_for
		)
		select $1, a.id, d.id, $4, $5, $6,
		       coalesce(nullif(d.scope, ''), 'default'), d.rootfs_key, d.image_digest,
		       $7, $8, $9, $9, $10::uuid, $11::timestamptz
		  from apps a
		  join deployments d on d.app_id = a.id
		 where a.id = $2
		   and a.account_id = $1
		   and a.status <> 'deleted'
		   and d.id = $3
		   and d.rootfs_key is not null
		   and d.rootfs_key <> ''
		   and d.image_digest <> ''
		   and d.status in ('imaging', 'snapshotting', 'live', 'superseded')
		returning `+appTaskSelectColumns,
		resolved.AccountID, resolved.AppID, resolved.DeploymentID, string(resolved.Kind),
		resolved.Command, resolved.CommandShell, resolved.TimeoutSeconds,
		resolved.MaxOutputBytes, resolved.CreatedAt, nullableCronID(resolved.CronID), resolved.ScheduledFor)
	task, err := scanAppTask(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return AppTask{}, ErrAppTaskDeploymentUnavailable
	}
	if err != nil {
		return AppTask{}, mapErr(err)
	}
	return task, nil
}

// CreateScheduledCronAppTask atomically advances a command cron's cursor,
// chooses the app's currently-live deployment, and queues its task. A stale
// scheduler snapshot becomes a no-op; a deployment change between schedule
// evaluation and persistence is resolved against the live row in this tx.
func (s *PgStore) CreateScheduledCronAppTask(ctx context.Context, cronID string, expectedLastFiredAt *time.Time, firedAt time.Time) (AppTask, bool, error) {
	if cronID == "" || firedAt.IsZero() {
		return AppTask{}, false, ErrAppTaskInvalid
	}
	firedAt = firedAt.UTC()
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return AppTask{}, false, fmt.Errorf("state: begin scheduled cron task: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var appID string
	var command []string
	var commandShell bool
	var timeoutSeconds, maxOutputBytes, retryMax, retryBackoffSeconds int
	err = tx.QueryRow(ctx, `
		update crons
		   set last_fired_at = $3
		 where id = $1::uuid
		   and enabled = true
		   and suspended_reason = ''
		   and cardinality(command) > 0
		   and last_fired_at is not distinct from $2::timestamptz
		   and ($2::timestamptz is null or $3 > $2::timestamptz)
		 returning app_id, command, command_shell, command_timeout_seconds,
		           command_max_output_bytes, retry_max, retry_backoff_seconds`, cronID, expectedLastFiredAt, firedAt).
		Scan(&appID, &command, &commandShell, &timeoutSeconds, &maxOutputBytes, &retryMax, &retryBackoffSeconds)
	if errors.Is(err, pgx.ErrNoRows) {
		return AppTask{}, false, nil
	}
	if err != nil {
		return AppTask{}, false, fmt.Errorf("state: claim command cron %s: %w", cronID, err)
	}

	var accountID, deploymentID, scope, artifactKey, imageDigest string
	err = tx.QueryRow(ctx, `
		select a.account_id, d.id, coalesce(nullif(d.scope, ''), 'default'), d.rootfs_key, d.image_digest
		  from apps a
		  join deployments d on d.app_id = a.id
		 where a.id = $1::uuid
		   and a.status <> 'deleted'
		   and d.status = 'live'
		   and d.rootfs_key is not null and d.rootfs_key <> ''
		   and d.image_digest <> ''
		 order by (d.traffic_percent > 0) desc, d.created_at desc, d.id desc
		 limit 1
		 for update of d`, appID).
		Scan(&accountID, &deploymentID, &scope, &artifactKey, &imageDigest)
	if errors.Is(err, pgx.ErrNoRows) {
		return AppTask{}, false, ErrAppTaskDeploymentUnavailable
	}
	if err != nil {
		return AppTask{}, false, fmt.Errorf("state: select live deployment for command cron %s: %w", cronID, err)
	}

	task, err := scanAppTask(tx.QueryRow(ctx, `
		insert into app_tasks (
			account_id, app_id, deployment_id, kind, command, command_shell,
			deployment_scope, artifact_key, image_digest, timeout_seconds,
			max_output_bytes, retry_max, retry_backoff_seconds, created_at, updated_at, cron_id, scheduled_for
		) values ($1::uuid, $2::uuid, $3::uuid, 'cron', $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $13, $14::uuid, $13)
		returning `+appTaskSelectColumns,
		accountID, appID, deploymentID, command, commandShell, scope, artifactKey,
		imageDigest, timeoutSeconds, maxOutputBytes, retryMax, retryBackoffSeconds, firedAt, cronID))
	if err != nil {
		return AppTask{}, false, fmt.Errorf("state: create scheduled app task for cron %s: %w", cronID, mapErr(err))
	}
	if err := tx.Commit(ctx); err != nil {
		return AppTask{}, false, fmt.Errorf("state: commit scheduled app task for cron %s: %w", cronID, err)
	}
	return task, true, nil
}

func (s *PgStore) CountActiveCronAppTasks(ctx context.Context, cronID string) (int, error) {
	var count int
	if err := s.pool.QueryRow(ctx, `
		select count(*) from app_tasks
		 where cron_id = $1::uuid and status in ('queued', 'restoring', 'running')`, cronID).Scan(&count); err != nil {
		return 0, mapErr(err)
	}
	return count, nil
}

func (s *PgStore) ListCronAppTaskRuns(ctx context.Context, cronID string, limit int, before string) ([]AppTask, error) {
	if limit <= 0 {
		limit = 10
	}
	var rows pgx.Rows
	var err error
	if before == "" {
		rows, err = s.pool.Query(ctx, `select `+appTaskSelectColumns+`
			from app_tasks where cron_id = $1::uuid
			order by created_at desc, id desc limit $2`, cronID, limit)
	} else {
		rows, err = s.pool.Query(ctx, `select `+appTaskSelectColumns+`
			from app_tasks where cron_id = $1::uuid
			  and (created_at, id) < (select created_at, id from app_tasks where id = $2::uuid and cron_id = $1::uuid)
			order by created_at desc, id desc limit $3`, cronID, before, limit)
	}
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	return scanAppTaskRows(rows)
}

func (s *PgStore) AppTaskByID(ctx context.Context, accountID, appID, taskID string) (AppTask, error) {
	task, err := scanAppTask(s.pool.QueryRow(ctx, `
		select `+appTaskSelectColumns+`
		  from app_tasks
		 where account_id = $1 and app_id = $2 and id = $3`,
		accountID, appID, taskID))
	if err != nil {
		return AppTask{}, mapErr(err)
	}
	return task, nil
}

func (s *PgStore) ReleaseAppTaskByDeployment(ctx context.Context, deploymentID string) (AppTask, error) {
	task, err := scanAppTask(s.pool.QueryRow(ctx, `
		select `+appTaskSelectColumns+`
		  from app_tasks
		 where deployment_id = $1 and kind = 'release'`, deploymentID))
	if err != nil {
		return AppTask{}, mapErr(err)
	}
	return task, nil
}

func enqueueTerminalReleaseTask(ctx context.Context, tx pgx.Tx, task AppTask) error {
	if task.Kind != AppTaskKindRelease || !task.Status.Terminal() {
		return nil
	}
	payload, err := json.Marshal(db.AppTaskChangedPayload{
		AccountID: task.AccountID, AppID: task.AppID, DeploymentID: task.DeploymentID,
		TaskID: task.ID, Kind: string(task.Kind), Status: string(task.Status),
	})
	if err != nil {
		return fmt.Errorf("state: marshal release task notification: %w", err)
	}
	if err := db.EnqueueDurableNotificationTx(ctx, tx, db.NotifyAppTaskChanged, string(payload)); err != nil {
		return fmt.Errorf("state: enqueue release task notification: %w", err)
	}
	return nil
}

func (s *PgStore) ListAppTasks(ctx context.Context, accountID, appID string, limit, offset int) ([]AppTask, error) {
	limit, offset = normalizeAppTaskPage(limit, offset)
	rows, err := s.pool.Query(ctx, `
		select `+appTaskSelectColumns+`
		  from app_tasks
		 where account_id = $1 and app_id = $2
		 order by created_at desc, id desc
		 limit $3 offset $4`, accountID, appID, limit, offset)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	tasks, err := scanAppTaskRows(rows)
	if err != nil {
		return nil, mapErr(err)
	}
	return tasks, nil
}

func (s *PgStore) ClaimNextAppTask(ctx context.Context, owner string, claimedAt time.Time, leaseDuration time.Duration) (AppTask, error) {
	if owner == "" || leaseDuration <= 0 || claimedAt.IsZero() {
		return AppTask{}, ErrAppTaskInvalid
	}
	claimedAt = claimedAt.UTC()
	expiresAt := claimedAt.Add(leaseDuration)
	task, err := scanAppTask(s.pool.QueryRow(ctx, `
		with candidate as (
			select id
			  from app_tasks
			 where status = 'queued'
			   and cancel_requested_at is null
			   and created_at <= $2
			   and (retry_at is null or retry_at <= $2)
			 order by coalesce(retry_at, created_at), created_at, id
			 for update skip locked
			 limit 1
		)
		update app_tasks as task
		   set status = 'restoring',
		       lease_token = gen_random_uuid(),
		       lease_owner = $1,
		       lease_expires_at = $3,
		       retry_at = null,
		       stdout_tail = '', stderr_tail = '', output_truncated = false,
		       exit_code = null, failure_code = null, failure_message = null,
		       updated_at = $2
		  from candidate
		 where task.id = candidate.id
		returning `+prefixedAppTaskColumns("task"), owner, claimedAt, expiresAt))
	if errors.Is(err, pgx.ErrNoRows) {
		return AppTask{}, ErrNotFound
	}
	if err != nil {
		return AppTask{}, mapErr(err)
	}
	return task, nil
}

func (s *PgStore) MarkAppTaskRunning(ctx context.Context, taskID, leaseToken string, startedAt time.Time) (AppTask, error) {
	if taskID == "" || leaseToken == "" || startedAt.IsZero() {
		return AppTask{}, ErrAppTaskInvalid
	}
	startedAt = startedAt.UTC()
	task, err := scanAppTask(s.pool.QueryRow(ctx, `
		update app_tasks
		   set status = 'running', started_at = $3, attempt_count = attempt_count + 1,
		       retry_at = null, stdout_tail = '', stderr_tail = '', output_truncated = false,
		       exit_code = null, failure_code = null, failure_message = null, updated_at = $3
		 where id = $1
		   and status = 'restoring'
		   and lease_token = $2
		   and cancel_requested_at is null
		   and lease_expires_at > $3
		returning `+appTaskSelectColumns, taskID, leaseToken, startedAt))
	if errors.Is(err, pgx.ErrNoRows) {
		return AppTask{}, ErrAppTaskLeaseLost
	}
	if err != nil {
		return AppTask{}, mapErr(err)
	}
	return task, nil
}

func (s *PgStore) RenewAppTaskLease(ctx context.Context, taskID, leaseToken string, renewedAt time.Time, leaseDuration time.Duration) error {
	if taskID == "" || leaseToken == "" || renewedAt.IsZero() || leaseDuration <= 0 {
		return ErrAppTaskInvalid
	}
	renewedAt = renewedAt.UTC()
	commandTag, err := s.pool.Exec(ctx, `
		update app_tasks
		   set lease_expires_at = $4, updated_at = $3
		 where id = $1
		   and lease_token = $2
		   and status in ('restoring', 'running')
		   and cancel_requested_at is null
		   and lease_expires_at > $3`,
		taskID, leaseToken, renewedAt, renewedAt.Add(leaseDuration))
	if err != nil {
		return mapErr(err)
	}
	if commandTag.RowsAffected() == 0 {
		return ErrAppTaskLeaseLost
	}
	return nil
}

func (s *PgStore) RequestAppTaskCancellation(ctx context.Context, accountID, appID, taskID string, requestedAt time.Time) (AppTask, error) {
	if requestedAt.IsZero() {
		requestedAt = time.Now().UTC()
	} else {
		requestedAt = requestedAt.UTC()
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return AppTask{}, fmt.Errorf("cancel app task: begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	current, err := scanAppTask(tx.QueryRow(ctx, `
		select `+appTaskSelectColumns+`
		  from app_tasks
		 where account_id = $1 and app_id = $2 and id = $3
		 for update`, accountID, appID, taskID))
	if err != nil {
		return AppTask{}, mapErr(err)
	}
	task, err := scanAppTask(tx.QueryRow(ctx, `
		update app_tasks
		   set status = case when status = 'queued' then 'cancelled' else status end,
		       retry_at = case when status = 'queued' then null else retry_at end,
		       failure_code = case when status = 'queued' then null else failure_code end,
		       failure_message = case when status = 'queued' then null else failure_message end,
		       cancel_requested_at = case
		           when status in ('restoring', 'running') then coalesce(cancel_requested_at, $4)
		           else cancel_requested_at
		       end,
		       finished_at = case when status = 'queued' then $4 else finished_at end,
		       updated_at = case
		           when status in ('queued', 'restoring', 'running') then $4
		           else updated_at
		       end
		 where account_id = $1 and app_id = $2 and id = $3
		returning `+appTaskSelectColumns, accountID, appID, taskID, requestedAt))
	if err != nil {
		return AppTask{}, mapErr(err)
	}
	if !current.Status.Terminal() {
		if err := enqueueTerminalReleaseTask(ctx, tx, task); err != nil {
			return AppTask{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return AppTask{}, fmt.Errorf("cancel app task: commit: %w", err)
	}
	return task, nil
}

func (s *PgStore) CompleteAppTask(ctx context.Context, params CompleteAppTaskParams) (AppTask, error) {
	if params.ID == "" || params.LeaseToken == "" || params.FinishedAt.IsZero() {
		return AppTask{}, ErrAppTaskInvalid
	}
	params.FinishedAt = params.FinishedAt.UTC()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return AppTask{}, fmt.Errorf("complete app task: begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	current, err := scanAppTask(tx.QueryRow(ctx, `
		select `+appTaskSelectColumns+`
		  from app_tasks where id = $1 for update`, params.ID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return AppTask{}, ErrAppTaskLeaseLost
		}
		return AppTask{}, mapErr(err)
	}
	if current.Status != AppTaskRestoring && current.Status != AppTaskRunning {
		return AppTask{}, ErrAppTaskLeaseLost
	}
	if current.LeaseToken == nil || *current.LeaseToken != params.LeaseToken ||
		current.LeaseExpiresAt == nil || !current.LeaseExpiresAt.After(params.FinishedAt) {
		return AppTask{}, ErrAppTaskLeaseLost
	}
	if params.FinishedAt.Before(current.CreatedAt) {
		return AppTask{}, fmt.Errorf("%w: finished_at precedes admission", ErrAppTaskInvalid)
	}
	if err := validateCompleteAppTask(params, current.MaxOutputBytes); err != nil {
		return AppTask{}, err
	}
	if current.CancelRequested != nil && params.Status != AppTaskCancelled {
		return AppTask{}, ErrAppTaskCancellationPending
	}
	if params.Status == AppTaskSucceeded && current.Status != AppTaskRunning {
		return AppTask{}, fmt.Errorf("%w: a task must be running before it can succeed", ErrAppTaskInvalid)
	}
	retryAt := cronAppTaskRetryAt(current, current.Status, params.Status, params.FinishedAt)
	nextStatus := params.Status
	var finishedAtArg any = params.FinishedAt
	var startedAtArg any
	if current.StartedAt != nil && !current.StartedAt.IsZero() {
		startedAtArg = *current.StartedAt
	}
	if retryAt != nil {
		nextStatus = AppTaskQueued
		finishedAtArg = nil
		startedAtArg = nil
	}
	completed, err := scanAppTask(tx.QueryRow(ctx, `
		update app_tasks
		   set status = $3,
		       stdout_tail = $4,
		       stderr_tail = $5,
		       output_truncated = $6,
		       exit_code = $7,
		       failure_code = $8,
		       failure_message = $9,
		       finished_at = $10,
		       retry_at = $11,
		       updated_at = $12,
		       started_at = $13,
		       lease_token = null,
		       lease_owner = null,
		       lease_expires_at = null
		 where id = $1 and lease_token = $2
		returning `+appTaskSelectColumns,
		params.ID, params.LeaseToken, string(nextStatus), params.StdoutTail,
		params.StderrTail, params.OutputTruncated, params.ExitCode,
		params.FailureCode, params.FailureMessage, finishedAtArg, retryAt, params.FinishedAt, startedAtArg))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return AppTask{}, ErrAppTaskLeaseLost
		}
		return AppTask{}, mapErr(err)
	}
	if err := enqueueTerminalReleaseTask(ctx, tx, completed); err != nil {
		return AppTask{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return AppTask{}, fmt.Errorf("complete app task: commit: %w", err)
	}
	return completed, nil
}

func (s *PgStore) SweepExpiredAppTasks(ctx context.Context, at time.Time) (AppTaskSweepResult, error) {
	if at.IsZero() {
		return AppTaskSweepResult{}, ErrAppTaskInvalid
	}
	at = at.UTC()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return AppTaskSweepResult{}, fmt.Errorf("sweep app tasks: begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	var result AppTaskSweepResult
	rows, err := tx.Query(ctx, `
		with expired as materialized (
			select id, status as old_status, cancel_requested_at
			  from app_tasks
			 where status in ('restoring', 'running')
			   and lease_expires_at <= $1
			 for update skip locked
		), updated as (
			update app_tasks as task
			   set status = case
			       when expired.cancel_requested_at is not null then 'cancelled'
			       when expired.old_status = 'restoring' then 'queued'
			       else 'failed'
			   end,
			       lease_token = null,
			       lease_owner = null,
			       lease_expires_at = null,
			       failure_code = case
			           when expired.old_status = 'running' and expired.cancel_requested_at is null
			           then 'lease_expired' else null end,
			       failure_message = case
			           when expired.old_status = 'running' and expired.cancel_requested_at is null
			           then 'the app task worker lease expired after dispatch; the command was not replayed'
			           else null end,
			       finished_at = case
			           when expired.cancel_requested_at is not null or expired.old_status = 'running'
			           then $1 else null end,
			       updated_at = $1
			  from expired
			 where task.id = expired.id
			returning task.id, task.account_id, task.app_id, task.deployment_id,
			          task.kind, task.status, expired.old_status,
			          expired.cancel_requested_at is not null as was_cancelled
		)
		select id, account_id, app_id, deployment_id, kind, status, old_status, was_cancelled
		  from updated`, at)
	if err != nil {
		return AppTaskSweepResult{}, mapErr(err)
	}
	defer rows.Close()
	terminalReleaseTasks := make([]AppTask, 0)
	for rows.Next() {
		var id, accountID, appID, deploymentID pgtype.UUID
		var task AppTask
		var oldStatus AppTaskStatus
		var wasCancelled bool
		if err := rows.Scan(&id, &accountID, &appID, &deploymentID, &task.Kind, &task.Status, &oldStatus, &wasCancelled); err != nil {
			return AppTaskSweepResult{}, mapErr(err)
		}
		task.ID = pgUUIDString(id)
		task.AccountID = pgUUIDString(accountID)
		task.AppID = pgUUIDString(appID)
		task.DeploymentID = pgUUIDString(deploymentID)
		switch {
		case wasCancelled:
			result.Cancelled++
		case oldStatus == AppTaskRestoring:
			result.RequeuedRestores++
		case oldStatus == AppTaskRunning:
			result.FailedRuns++
		}
		if task.Kind == AppTaskKindRelease && task.Status.Terminal() {
			terminalReleaseTasks = append(terminalReleaseTasks, task)
		}
	}
	if err := rows.Err(); err != nil {
		return AppTaskSweepResult{}, mapErr(err)
	}
	rows.Close()
	for _, task := range terminalReleaseTasks {
		if err := enqueueTerminalReleaseTask(ctx, tx, task); err != nil {
			return AppTaskSweepResult{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return AppTaskSweepResult{}, fmt.Errorf("sweep app tasks: commit: %w", err)
	}
	return result, nil
}

func prefixedAppTaskColumns(alias string) string {
	return alias + `.id, ` + alias + `.account_id, ` + alias + `.app_id, ` + alias + `.deployment_id, ` + alias + `.kind,
       ` + alias + `.command, ` + alias + `.command_shell, ` + alias + `.deployment_scope, ` + alias + `.artifact_key, ` + alias + `.image_digest,
       ` + alias + `.status, ` + alias + `.timeout_seconds, ` + alias + `.max_output_bytes,
       ` + alias + `.lease_token, ` + alias + `.lease_owner, ` + alias + `.lease_expires_at, ` + alias + `.cancel_requested_at,
       ` + alias + `.stdout_tail, ` + alias + `.stderr_tail, ` + alias + `.output_truncated, ` + alias + `.exit_code,
       ` + alias + `.failure_code, ` + alias + `.failure_message, ` + alias + `.started_at, ` + alias + `.finished_at,
       ` + alias + `.created_at, ` + alias + `.updated_at, ` + alias + `.cron_id, ` + alias + `.scheduled_for`
}
