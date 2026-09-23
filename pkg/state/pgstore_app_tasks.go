package state

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

var _ AppTaskStore = (*PgStore)(nil)

const appTaskSelectColumns = `id, account_id, app_id, deployment_id, kind,
       command, command_shell, deployment_scope, artifact_key, image_digest,
       status, timeout_seconds, max_output_bytes,
       lease_token, lease_owner, lease_expires_at, cancel_requested_at,
       stdout_tail, stderr_tail, output_truncated, exit_code,
       failure_code, failure_message, started_at, finished_at,
       created_at, updated_at`

type appTaskRowScanner interface {
	Scan(dest ...any) error
}

func scanAppTask(row appTaskRowScanner) (AppTask, error) {
	var task AppTask
	var id, accountID, appID, deploymentID pgtype.UUID
	var leaseToken pgtype.UUID
	var leaseOwner, failureCode, failureMessage pgtype.Text
	var leaseExpiresAt, cancelRequestedAt, startedAt, finishedAt pgtype.Timestamptz
	var exitCode pgtype.Int4
	if err := row.Scan(
		&id, &accountID, &appID, &deploymentID, &task.Kind,
		&task.Command, &task.CommandShell, &task.DeploymentScope, &task.ArtifactKey, &task.ImageDigest,
		&task.Status, &task.TimeoutSeconds, &task.MaxOutputBytes,
		&leaseToken, &leaseOwner, &leaseExpiresAt, &cancelRequestedAt,
		&task.StdoutTail, &task.StderrTail, &task.OutputTruncated, &exitCode,
		&failureCode, &failureMessage, &startedAt, &finishedAt,
		&task.CreatedAt, &task.UpdatedAt,
	); err != nil {
		return AppTask{}, err
	}
	task.ID = pgUUIDString(id)
	task.AccountID = pgUUIDString(accountID)
	task.AppID = pgUUIDString(appID)
	task.DeploymentID = pgUUIDString(deploymentID)
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
			max_output_bytes, created_at, updated_at
		)
		select $1, a.id, d.id, $4, $5, $6,
		       coalesce(nullif(d.scope, ''), 'default'), d.rootfs_key, d.image_digest,
		       $7, $8, $9, $9
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
		resolved.MaxOutputBytes, resolved.CreatedAt)
	task, err := scanAppTask(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return AppTask{}, ErrAppTaskDeploymentUnavailable
	}
	if err != nil {
		return AppTask{}, mapErr(err)
	}
	return task, nil
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
			 order by created_at, id
			 for update skip locked
			 limit 1
		)
		update app_tasks as task
		   set status = 'restoring',
		       lease_token = gen_random_uuid(),
		       lease_owner = $1,
		       lease_expires_at = $3,
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
		   set status = 'running', started_at = $3, updated_at = $3
		 where id = $1
		   and status = 'restoring'
		   and lease_token = $2
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

func (s *PgStore) RequestAppTaskCancellation(ctx context.Context, accountID, appID, taskID string, requestedAt time.Time) (AppTask, error) {
	if requestedAt.IsZero() {
		requestedAt = time.Now().UTC()
	} else {
		requestedAt = requestedAt.UTC()
	}
	task, err := scanAppTask(s.pool.QueryRow(ctx, `
		update app_tasks
		   set status = case when status = 'queued' then 'cancelled' else status end,
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
	if params.Status == AppTaskSucceeded && current.Status != AppTaskRunning {
		return AppTask{}, fmt.Errorf("%w: a task must be running before it can succeed", ErrAppTaskInvalid)
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
		       updated_at = $10,
		       lease_token = null,
		       lease_owner = null,
		       lease_expires_at = null
		 where id = $1 and lease_token = $2
		returning `+appTaskSelectColumns,
		params.ID, params.LeaseToken, string(params.Status), params.StdoutTail,
		params.StderrTail, params.OutputTruncated, params.ExitCode,
		params.FailureCode, params.FailureMessage, params.FinishedAt))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return AppTask{}, ErrAppTaskLeaseLost
		}
		return AppTask{}, mapErr(err)
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
	var result AppTaskSweepResult
	err := s.pool.QueryRow(ctx, `
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
			returning expired.old_status, expired.cancel_requested_at
		)
		select
			count(*) filter (where old_status = 'restoring' and cancel_requested_at is null),
			count(*) filter (where old_status = 'running' and cancel_requested_at is null),
			count(*) filter (where cancel_requested_at is not null)
		  from updated`, at).Scan(&result.RequeuedRestores, &result.FailedRuns, &result.Cancelled)
	if err != nil {
		return AppTaskSweepResult{}, mapErr(err)
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
       ` + alias + `.created_at, ` + alias + `.updated_at`
}
