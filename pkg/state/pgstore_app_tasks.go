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
	"github.com/onebox-faas/faas/pkg/exclusivework"
	"github.com/onebox-faas/faas/pkg/workpolicy"
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
       created_at, updated_at, cron_id, scheduled_for,
       failure_rules, occurrence_id, start_deadline_at, work_decision, outcome_code, exclusive_operation_id, exclusive_generation`

type appTaskRowScanner interface {
	Scan(dest ...any) error
}

func scanAppTask(row appTaskRowScanner) (AppTask, error) {
	var task AppTask
	var id, accountID, appID, deploymentID pgtype.UUID
	var cronID pgtype.UUID
	var occurrenceID pgtype.UUID
	var exclusiveOperationID pgtype.UUID
	var exclusiveGeneration pgtype.Int8
	var leaseToken pgtype.UUID
	var leaseOwner, failureCode, failureMessage pgtype.Text
	var leaseExpiresAt, cancelRequestedAt, startedAt, finishedAt, scheduledFor, retryAt, startDeadlineAt pgtype.Timestamptz
	var exitCode pgtype.Int4
	var failureRules, workDecision []byte
	if err := row.Scan(
		&id, &accountID, &appID, &deploymentID, &task.Kind,
		&task.Command, &task.CommandShell, &task.DeploymentScope, &task.ArtifactKey, &task.ImageDigest,
		&task.Status, &task.TimeoutSeconds, &task.MaxOutputBytes,
		&task.RetryMax, &task.RetryBackoffSeconds, &task.AttemptCount, &retryAt,
		&leaseToken, &leaseOwner, &leaseExpiresAt, &cancelRequestedAt,
		&task.StdoutTail, &task.StderrTail, &task.OutputTruncated, &exitCode,
		&failureCode, &failureMessage, &startedAt, &finishedAt,
		&task.CreatedAt, &task.UpdatedAt, &cronID, &scheduledFor,
		&failureRules, &occurrenceID, &startDeadlineAt, &workDecision, &task.OutcomeCode,
		&exclusiveOperationID, &exclusiveGeneration,
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
	if exclusiveOperationID.Valid {
		task.ExclusiveOperationID = pgUUIDString(exclusiveOperationID)
	}
	if exclusiveGeneration.Valid {
		task.ExclusiveGeneration = exclusiveGeneration.Int64
	}
	task.ScheduledFor = timestamptzToTimePtr(scheduledFor)
	if len(failureRules) > 0 {
		if err := json.Unmarshal(failureRules, &task.FailureRules); err != nil {
			return AppTask{}, err
		}
	}
	if occurrenceID.Valid {
		task.OccurrenceID = pgUUIDString(occurrenceID)
	}
	task.StartDeadlineAt = timestamptzToTimePtr(startDeadlineAt)
	if len(workDecision) > 0 {
		if err := json.Unmarshal(workDecision, &task.WorkDecision); err != nil {
			return AppTask{}, err
		}
	}
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
			max_output_bytes, created_at, updated_at, cron_id, scheduled_for,
			failure_rules, occurrence_id, start_deadline_at, exclusive_operation_id, exclusive_generation
		)
		select $1, a.id, d.id, $4, $5, $6,
		       coalesce(nullif(d.scope, ''), 'default'), d.rootfs_key, d.image_digest,
		       $7, $8, $9, $9, $10::uuid, $11::timestamptz,
		       $12::jsonb, $13::uuid, $14::timestamptz, $15::uuid, $16::bigint
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
		   and ($15::uuid is null or exists (
		       select 1 from exclusive_work_operations operation
		        where operation.id = $15::uuid and operation.account_id = a.account_id
		          and operation.app_id = a.id and operation.state = 'running'
		          and operation.generation = $16::bigint
		          and operation.lease_expires_at > clock_timestamp()
		          and operation.attempt_deadline > clock_timestamp()
		   ))
		returning `+appTaskSelectColumns,
		resolved.AccountID, resolved.AppID, resolved.DeploymentID, string(resolved.Kind),
		resolved.Command, resolved.CommandShell, resolved.TimeoutSeconds,
		resolved.MaxOutputBytes, resolved.CreatedAt, nullableCronID(resolved.CronID), resolved.ScheduledFor,
		policyJSON(resolved.FailureRules), nullableCronID(resolved.OccurrenceID), resolved.StartDeadlineAt,
		nullableCronID(resolved.ExclusiveOperationID), nullableGeneration(resolved.ExclusiveGeneration))
	task, err := scanAppTask(row)
	if errors.Is(err, pgx.ErrNoRows) {
		if resolved.ExclusiveOperationID != "" {
			return AppTask{}, exclusivework.ErrStaleOwner
		}
		return AppTask{}, ErrAppTaskDeploymentUnavailable
	}
	if err != nil {
		return AppTask{}, mapErr(err)
	}
	return task, nil
}

func nullableGeneration(generation int64) any {
	if generation <= 0 {
		return nil
	}
	return generation
}

func (s *PgStore) ListAppTasksByExclusiveOperation(ctx context.Context, accountID, operationID string) ([]AppTask, error) {
	rows, err := s.pool.Query(ctx, `select `+appTaskSelectColumns+`
	  from app_tasks where account_id = $1 and exclusive_operation_id = $2
	 order by exclusive_generation, created_at, id`, accountID, operationID)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	return scanAppTaskRows(rows)
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
	var schedulePolicyRaw, failureRulesRaw []byte
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
		           command_max_output_bytes, retry_max, retry_backoff_seconds,
	           schedule_policy, failure_rules`, cronID, expectedLastFiredAt, firedAt).
		Scan(&appID, &command, &commandShell, &timeoutSeconds, &maxOutputBytes, &retryMax, &retryBackoffSeconds,
			&schedulePolicyRaw, &failureRulesRaw)
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

	var schedulePolicy *workpolicy.SchedulePolicy
	if len(schedulePolicyRaw) > 0 {
		var policy workpolicy.SchedulePolicy
		if err := json.Unmarshal(schedulePolicyRaw, &policy); err != nil {
			return AppTask{}, false, fmt.Errorf("state: decode cron schedule policy: %w", err)
		}
		schedulePolicy = &policy
	}
	var deadline *time.Time
	if schedulePolicy != nil {
		deadline = schedulePolicy.Deadline(firedAt)
	}
	task, err := scanAppTask(tx.QueryRow(ctx, `
		insert into app_tasks (
			account_id, app_id, deployment_id, kind, command, command_shell,
			deployment_scope, artifact_key, image_digest, timeout_seconds,
			max_output_bytes, retry_max, retry_backoff_seconds, created_at, updated_at, cron_id, scheduled_for,
			failure_rules, start_deadline_at
		) values ($1::uuid, $2::uuid, $3::uuid, 'cron', $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $13, $14::uuid, $13, $15::jsonb, $16)
		returning `+appTaskSelectColumns,
		accountID, appID, deploymentID, command, commandShell, scope, artifactKey,
		imageDigest, timeoutSeconds, maxOutputBytes, retryMax, retryBackoffSeconds, firedAt, cronID,
		failureRulesRaw, deadline))
	if err != nil {
		return AppTask{}, false, fmt.Errorf("state: create scheduled app task for cron %s: %w", cronID, mapErr(err))
	}
	if err := tx.Commit(ctx); err != nil {
		return AppTask{}, false, fmt.Errorf("state: commit scheduled app task for cron %s: %w", cronID, err)
	}
	return task, true, nil
}

// CreateScheduledCronAppTaskOccurrence atomically persists the occurrence
// decision and, when admitted, its command task. The cron row lock fences
// duplicate schedulers and policy edits against the cursor advance.
func (s *PgStore) CreateScheduledCronAppTaskOccurrence(ctx context.Context, cronID string, expectedLastFiredAt *time.Time, evaluatedAt time.Time, options CronScheduledOccurrenceOptions) (AppTask, ScheduleOccurrence, bool, error) {
	if cronID == "" || evaluatedAt.IsZero() {
		return AppTask{}, ScheduleOccurrence{}, false, ErrAppTaskInvalid
	}
	evaluatedAt = evaluatedAt.UTC()
	scheduledFor := options.ScheduledFor.UTC()
	if options.ScheduledFor.IsZero() {
		scheduledFor = evaluatedAt
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return AppTask{}, ScheduleOccurrence{}, false, fmt.Errorf("state: begin scheduled cron occurrence: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var cron Cron
	var accountID string
	var enabled bool
	var skipIfRunning bool
	var suspendedReason string
	var lastFiredAt pgtype.Timestamptz
	var schedulePolicyRaw, failureRulesRaw []byte
	err = tx.QueryRow(ctx, `
		select c.app_id::text, a.account_id::text, c.command, c.command_shell,
		       c.command_timeout_seconds, c.command_max_output_bytes,
		       c.retry_max, c.retry_backoff_seconds, c.enabled, c.skip_if_running,
		       c.suspended_reason, c.last_fired_at, c.schedule_revision,
		       c.schedule_policy, c.failure_rules
		  from crons c join apps a on a.id = c.app_id
		 where c.id = $1::uuid and a.status <> 'deleted'
		 for update of c`, cronID).Scan(
		&cron.AppID, &accountID, &cron.Command, &cron.CommandShell,
		&cron.CommandTimeoutSeconds, &cron.CommandMaxOutputBytes,
		&cron.RetryMax, &cron.RetryBackoffSeconds, &enabled, &skipIfRunning,
		&suspendedReason, &lastFiredAt, &cron.ScheduleRevision,
		&schedulePolicyRaw, &failureRulesRaw)
	if errors.Is(err, pgx.ErrNoRows) {
		return AppTask{}, ScheduleOccurrence{}, false, nil
	}
	if err != nil {
		return AppTask{}, ScheduleOccurrence{}, false, fmt.Errorf("state: lock scheduled command cron %s: %w", cronID, err)
	}
	cron.ID, cron.Enabled, cron.SkipIfRunning, cron.SuspendedReason = cronID, enabled, skipIfRunning, suspendedReason
	if !enabled || suspendedReason != "" || len(cron.Command) == 0 ||
		!sameTimePointer(timestamptzToTimePtr(lastFiredAt), expectedLastFiredAt) ||
		(options.ScheduleRevision > 0 && cron.ScheduleRevision != options.ScheduleRevision) {
		return AppTask{}, ScheduleOccurrence{}, false, nil
	}
	if expectedLastFiredAt != nil && !scheduledFor.After(*expectedLastFiredAt) {
		return AppTask{}, ScheduleOccurrence{}, false, nil
	}
	if len(schedulePolicyRaw) > 0 {
		var policy workpolicy.SchedulePolicy
		if err := json.Unmarshal(schedulePolicyRaw, &policy); err != nil {
			return AppTask{}, ScheduleOccurrence{}, false, fmt.Errorf("state: decode cron schedule policy: %w", err)
		}
		cron.SchedulePolicy = &policy
	}
	if len(failureRulesRaw) > 0 {
		var rules workpolicy.FailureRules
		if err := json.Unmarshal(failureRulesRaw, &rules); err != nil {
			return AppTask{}, ScheduleOccurrence{}, false, fmt.Errorf("state: decode cron failure rules: %w", err)
		}
		cron.FailureRules = &rules
	}
	policy := effectiveCronSchedulePolicy(cron)
	deadline := policy.Deadline(scheduledFor)
	status, reason, blocker := "queued", "", ""
	if options.Disposition != "" {
		if options.Disposition != "coalesced" && options.Disposition != "missed_deadline" {
			return AppTask{}, ScheduleOccurrence{}, false, ErrInvalidArgument
		}
		status, reason = options.Disposition, options.Reason
	} else if workpolicy.DeadlineMissed(deadline, evaluatedAt) {
		status, reason = "missed_deadline", "start deadline expired before the scheduler could dispatch the occurrence"
	}
	if status == "queued" && policy.Overlap != "allow" {
		var active bool
		var activeOccurrence string
		err = tx.QueryRow(ctx, `
			select true, coalesce(occurrence_id::text, '') from app_tasks
			 where cron_id = $1::uuid and status in ('queued','restoring','running')
			 order by created_at asc, id asc limit 1`, cronID).Scan(&active, &activeOccurrence)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return AppTask{}, ScheduleOccurrence{}, false, fmt.Errorf("state: check active command cron tasks: %w", err)
		}
		if active {
			if policy.Overlap == "replace" {
				return AppTask{}, ScheduleOccurrence{}, false, nil
			}
			status, reason, blocker = "skipped_overlap", "an earlier task for this cron is still active", activeOccurrence
		}
	}

	var deploymentID, scope, artifactKey, imageDigest string
	if status == "queued" {
		err = tx.QueryRow(ctx, `
			select d.id::text, coalesce(nullif(d.scope, ''), 'default'), d.rootfs_key, d.image_digest
			  from deployments d
			 where d.app_id = $1::uuid and d.status = 'live'
			   and d.rootfs_key is not null and d.rootfs_key <> '' and d.image_digest <> ''
			 order by (d.traffic_percent > 0) desc, d.created_at desc, d.id desc
			 limit 1 for update`, cron.AppID).Scan(&deploymentID, &scope, &artifactKey, &imageDigest)
		if errors.Is(err, pgx.ErrNoRows) {
			return AppTask{}, ScheduleOccurrence{}, false, ErrAppTaskDeploymentUnavailable
		}
		if err != nil {
			return AppTask{}, ScheduleOccurrence{}, false, fmt.Errorf("state: select live deployment for scheduled cron: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `update crons set last_fired_at = $2 where id = $1::uuid`, cronID, scheduledFor); err != nil {
		return AppTask{}, ScheduleOccurrence{}, false, fmt.Errorf("state: advance scheduled cron cursor: %w", err)
	}
	var occurrenceID string
	if err := tx.QueryRow(ctx, `insert into schedule_occurrences (
		account_id, cron_id, schedule_revision, scheduled_for, start_deadline_at,
		 schedule_policy, status, reason, blocking_occurrence_id)
		values ($1::uuid,$2::uuid,$3,$4,$5,$6::jsonb,$7,$8,nullif($9,'')::uuid)
		returning id::text`, accountID, cronID, cron.ScheduleRevision, scheduledFor, deadline,
		policyJSON(policy), status, reason, blocker).Scan(&occurrenceID); err != nil {
		return AppTask{}, ScheduleOccurrence{}, false, fmt.Errorf("state: record scheduled cron occurrence: %w", mapErr(err))
	}
	occurrence := ScheduleOccurrence{
		ID: occurrenceID, AccountID: accountID, CronID: cronID,
		ScheduleRevision: cron.ScheduleRevision, ScheduledFor: scheduledFor,
		StartDeadlineAt: cloneTimePtr(deadline), SchedulePolicy: *workpolicy.Clone(policy),
		Status: status, Reason: reason, BlockingOccurrenceID: blocker,
		CreatedAt: evaluatedAt, UpdatedAt: evaluatedAt,
	}
	if status != "queued" {
		if err := tx.Commit(ctx); err != nil {
			return AppTask{}, ScheduleOccurrence{}, false, fmt.Errorf("state: commit skipped cron occurrence: %w", err)
		}
		return AppTask{}, occurrence, false, nil
	}
	task, err := scanAppTask(tx.QueryRow(ctx, `insert into app_tasks (
		 account_id, app_id, deployment_id, kind, command, command_shell,
		 deployment_scope, artifact_key, image_digest, timeout_seconds,
		 max_output_bytes, retry_max, retry_backoff_seconds, created_at, updated_at,
		 cron_id, scheduled_for, failure_rules, occurrence_id, start_deadline_at)
		values ($1::uuid,$2::uuid,$3::uuid,'cron',$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$13,
		 $14::uuid,$15,$16::jsonb,$17::uuid,$18)
		returning `+appTaskSelectColumns,
		accountID, cron.AppID, deploymentID, cron.Command, cron.CommandShell, scope, artifactKey,
		imageDigest, cron.CommandTimeoutSeconds, cron.CommandMaxOutputBytes, cron.RetryMax,
		cron.RetryBackoffSeconds, evaluatedAt, cronID, scheduledFor, policyJSON(cron.FailureRules), occurrenceID, deadline))
	if err != nil {
		return AppTask{}, ScheduleOccurrence{}, false, fmt.Errorf("state: create scheduled command task: %w", mapErr(err))
	}
	if _, err := tx.Exec(ctx, `update schedule_occurrences set app_task_id=$2::uuid where id=$1::uuid`, occurrenceID, task.ID); err != nil {
		return AppTask{}, ScheduleOccurrence{}, false, err
	}
	occurrence.AppTaskID = task.ID
	if err := tx.Commit(ctx); err != nil {
		return AppTask{}, ScheduleOccurrence{}, false, fmt.Errorf("state: commit scheduled command task: %w", err)
	}
	return task, occurrence, true, nil
}

// CreateManualCronAppTaskForFireNow creates a command task for a claimed
// fire-now request without updating crons.last_fired_at. The app task and
// the terminal request receipt commit atomically, making a scheduler retry
// safe if it loses the response after the transaction commits.
func (s *PgStore) CreateManualCronAppTaskForFireNow(ctx context.Context, requestID string, firedAt time.Time) (AppTask, error) {
	if requestID == "" || firedAt.IsZero() {
		return AppTask{}, ErrAppTaskInvalid
	}
	firedAt = firedAt.UTC()
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return AppTask{}, fmt.Errorf("state: begin manual cron task: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var cronID, accountID, requestStatus string
	var existingTaskID pgtype.UUID
	err = tx.QueryRow(ctx, `
		select cron_id::text, account_id::text, status, task_id
		  from cron_fire_now_requests
		 where id = $1::uuid
		 for update`, requestID).
		Scan(&cronID, &accountID, &requestStatus, &existingTaskID)
	if errors.Is(err, pgx.ErrNoRows) {
		return AppTask{}, ErrFireNowRequestNotFound
	}
	if err != nil {
		return AppTask{}, fmt.Errorf("state: load manual cron fire-now request: %w", err)
	}
	if existingTaskID.Valid {
		task, scanErr := scanAppTask(tx.QueryRow(ctx, `
			select `+appTaskSelectColumns+` from app_tasks where id = $1`, pgUUIDString(existingTaskID)))
		if scanErr != nil {
			return AppTask{}, fmt.Errorf("state: load manual cron task receipt: %w", mapErr(scanErr))
		}
		if err := tx.Commit(ctx); err != nil {
			return AppTask{}, fmt.Errorf("state: commit manual cron task receipt: %w", err)
		}
		return task, nil
	}
	if requestStatus != string(FireNowStatusRunning) {
		return AppTask{}, ErrFireNowRequestNotFound
	}

	var appID, cronAccountID string
	var command []string
	var commandShell, enabled, skipIfRunning bool
	var suspendedReason string
	var timeoutSeconds, maxOutputBytes, retryMax, retryBackoffSeconds int
	err = tx.QueryRow(ctx, `
		select c.app_id::text, a.account_id::text, c.command, c.command_shell,
		       c.command_timeout_seconds, c.command_max_output_bytes,
	       c.retry_max, c.retry_backoff_seconds, c.enabled, c.suspended_reason,
	       c.skip_if_running
		  from crons c
		  join apps a on a.id = c.app_id
		 where c.id = $1::uuid and a.status <> 'deleted'
		 for update of c`, cronID).
		Scan(&appID, &cronAccountID, &command, &commandShell,
			&timeoutSeconds, &maxOutputBytes, &retryMax, &retryBackoffSeconds,
			&enabled, &suspendedReason, &skipIfRunning)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && cronAccountID != accountID) {
		return AppTask{}, ErrNotFound
	}
	if err != nil {
		return AppTask{}, fmt.Errorf("state: lock manual command cron: %w", err)
	}
	if !enabled {
		return AppTask{}, ErrAppTaskCronDisabled
	}
	if suspendedReason != "" {
		return AppTask{}, ErrAppTaskCronSuspended
	}
	if len(command) == 0 {
		return AppTask{}, ErrAppTaskInvalid
	}
	if skipIfRunning {
		var active bool
		if err := tx.QueryRow(ctx, `
			select exists (
				select 1 from app_tasks
				 where cron_id = $1::uuid and status in ('queued', 'restoring', 'running')
			)`, cronID).Scan(&active); err != nil {
			return AppTask{}, fmt.Errorf("state: check active manual command cron: %w", err)
		}
		if active {
			return AppTask{}, ErrAppTaskCronOverlap
		}
	}

	var deploymentID, scope, artifactKey, imageDigest string
	err = tx.QueryRow(ctx, `
		select d.id::text, coalesce(nullif(d.scope, ''), 'default'), d.rootfs_key, d.image_digest
		  from deployments d
		 where d.app_id = $1::uuid and d.status = 'live'
		   and d.rootfs_key is not null and d.rootfs_key <> ''
		   and d.image_digest <> ''
		 order by (d.traffic_percent > 0) desc, d.created_at desc, d.id desc
		 limit 1 for update`, appID).
		Scan(&deploymentID, &scope, &artifactKey, &imageDigest)
	if errors.Is(err, pgx.ErrNoRows) {
		return AppTask{}, ErrAppTaskDeploymentUnavailable
	}
	if err != nil {
		return AppTask{}, fmt.Errorf("state: select live deployment for manual command cron: %w", err)
	}

	task, err := scanAppTask(tx.QueryRow(ctx, `
		insert into app_tasks (
			account_id, app_id, deployment_id, kind, command, command_shell,
			deployment_scope, artifact_key, image_digest, timeout_seconds,
			max_output_bytes, retry_max, retry_backoff_seconds, created_at, updated_at,
			cron_id, scheduled_for
		) values ($1::uuid, $2::uuid, $3::uuid, 'cron', $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $13, $14::uuid, null)
		returning `+appTaskSelectColumns,
		accountID, appID, deploymentID, command, commandShell, scope, artifactKey,
		imageDigest, timeoutSeconds, maxOutputBytes, retryMax, retryBackoffSeconds, firedAt, cronID))
	if err != nil {
		return AppTask{}, fmt.Errorf("state: create manual command cron task: %w", mapErr(err))
	}
	finishedAt := time.Now().UTC()
	tag, err := tx.Exec(ctx, `
		update cron_fire_now_requests
		   set status = 'succeeded', task_id = $2::uuid, finished_at = $3
		 where id = $1::uuid and status = 'running'`, requestID, task.ID, finishedAt)
	if err != nil {
		return AppTask{}, fmt.Errorf("state: complete manual command cron request: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return AppTask{}, ErrFireNowRequestNotFound
	}
	if err := tx.Commit(ctx); err != nil {
		return AppTask{}, fmt.Errorf("state: commit manual command cron task: %w", err)
	}
	return task, nil
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
	if _, err := s.pool.Exec(ctx, `
		update app_tasks task
		   set status = 'cancelled', retry_at = null, finished_at = $1, updated_at = $1
		 where task.status = 'queued' and task.start_deadline_at < $1
		   and (task.occurrence_id is null or not exists (
		       select 1 from schedule_occurrences occurrence
		        where occurrence.id = task.occurrence_id and occurrence.started_at is not null))`, claimedAt); err != nil {
		return AppTask{}, mapErr(err)
	}
	task, err := scanAppTask(s.pool.QueryRow(ctx, `
		with candidate as (
			select id
			  from app_tasks
			 where status = 'queued'
			   and cancel_requested_at is null
			   and created_at <= $2
			   and (retry_at is null or retry_at <= $2)
			   and (start_deadline_at is null or start_deadline_at >= $2 or exists (
			       select 1 from schedule_occurrences occurrence
			        where occurrence.id = app_tasks.occurrence_id and occurrence.started_at is not null))
			   and (exclusive_operation_id is null or exists (
			       select 1 from exclusive_work_operations operation
			        where operation.id = app_tasks.exclusive_operation_id
			          and operation.account_id = app_tasks.account_id
			          and operation.app_id = app_tasks.app_id
			          and operation.state = 'running'
			          and operation.generation = app_tasks.exclusive_generation
			          and operation.lease_expires_at > clock_timestamp()
			          and operation.attempt_deadline > clock_timestamp()
			   ))
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
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return AppTask{}, mapErr(err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	current, err := scanAppTask(tx.QueryRow(ctx, `select `+appTaskSelectColumns+` from app_tasks where id = $1 for update`, taskID))
	if err != nil || current.Status != AppTaskRestoring || current.LeaseToken == nil || *current.LeaseToken != leaseToken ||
		current.CancelRequested != nil || current.LeaseExpiresAt == nil || !current.LeaseExpiresAt.After(startedAt) {
		return AppTask{}, ErrAppTaskLeaseLost
	}
	if err := lockAppTaskExclusiveOwner(ctx, tx, current); err != nil {
		return AppTask{}, ErrAppTaskLeaseLost
	}
	task, err := scanAppTask(tx.QueryRow(ctx, `
		update app_tasks
		   set status = 'running', started_at = $3, attempt_count = attempt_count + 1,
		       retry_at = null, stdout_tail = '', stderr_tail = '', output_truncated = false,
		       exit_code = null, failure_code = null, failure_message = null, updated_at = $3
		 where id = $1
		   and status = 'restoring'
		   and lease_token = $2
		   and cancel_requested_at is null
		   and lease_expires_at > $3
		   and (start_deadline_at is null or start_deadline_at >= $3 or exists (
		       select 1 from schedule_occurrences occurrence
		        where occurrence.id = app_tasks.occurrence_id and occurrence.started_at is not null))
		returning `+appTaskSelectColumns, taskID, leaseToken, startedAt))
	if errors.Is(err, pgx.ErrNoRows) {
		tag, cancelErr := s.pool.Exec(ctx, `
			update app_tasks task
			   set status = 'cancelled', retry_at = null, lease_token = null,
			       lease_owner = null, lease_expires_at = null,
			       finished_at = $3, updated_at = $3
			 where task.id = $1 and task.status = 'restoring' and task.lease_token = $2
			   and task.cancel_requested_at is null and task.lease_expires_at > $3
			   and task.start_deadline_at < $3 and task.started_at is null
			   and (task.occurrence_id is null or not exists (
			       select 1 from schedule_occurrences occurrence
			        where occurrence.id = task.occurrence_id and occurrence.started_at is not null))`, taskID, leaseToken, startedAt)
		if cancelErr != nil {
			return AppTask{}, mapErr(cancelErr)
		}
		if tag.RowsAffected() == 0 {
			return AppTask{}, ErrAppTaskLeaseLost
		}
		return AppTask{}, ErrAppTaskLeaseLost
	}
	if err != nil {
		return AppTask{}, mapErr(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return AppTask{}, mapErr(err)
	}
	return task, nil
}

func (s *PgStore) RenewAppTaskLease(ctx context.Context, taskID, leaseToken string, renewedAt time.Time, leaseDuration time.Duration) error {
	if taskID == "" || leaseToken == "" || renewedAt.IsZero() || leaseDuration <= 0 {
		return ErrAppTaskInvalid
	}
	renewedAt = renewedAt.UTC()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return mapErr(err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	current, err := scanAppTask(tx.QueryRow(ctx, `select `+appTaskSelectColumns+` from app_tasks where id = $1 for update`, taskID))
	if err != nil || (current.Status != AppTaskRestoring && current.Status != AppTaskRunning) || current.LeaseToken == nil ||
		*current.LeaseToken != leaseToken || current.CancelRequested != nil || current.LeaseExpiresAt == nil || !current.LeaseExpiresAt.After(renewedAt) {
		return ErrAppTaskLeaseLost
	}
	if err := lockAppTaskExclusiveOwner(ctx, tx, current); err != nil {
		return ErrAppTaskLeaseLost
	}
	commandTag, err := tx.Exec(ctx, `
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
	return mapErr(tx.Commit(ctx))
}

func lockAppTaskExclusiveOwner(ctx context.Context, tx pgx.Tx, task AppTask) error {
	if task.ExclusiveOperationID == "" {
		return nil
	}
	var id string
	err := tx.QueryRow(ctx, `
		select id::text from exclusive_work_operations
		 where id = $1 and account_id = $2 and app_id = $3
		   and state = 'running' and generation = $4
		   and lease_expires_at > clock_timestamp()
		   and attempt_deadline > clock_timestamp()
		 for update`, task.ExclusiveOperationID, task.AccountID, task.AppID, task.ExclusiveGeneration).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return exclusivework.ErrStaleOwner
	}
	return mapErr(err)
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
	if err := lockAppTaskExclusiveOwner(ctx, tx, current); err != nil {
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
	decision := workpolicy.Evaluate(current.FailureRules, workpolicy.Evidence{
		Succeeded: params.Status == AppTaskSucceeded, Cancelled: params.Status == AppTaskCancelled,
		Infra: current.Status == AppTaskRestoring, ExitCode: params.ExitCode, OutcomeCode: params.OutcomeCode,
	})
	if decision.Reason == "outcome_code_matched" && params.Status == AppTaskSucceeded {
		params.Status = AppTaskFailed
		code, message := "classified_outcome", "command reported an application outcome classified by policy"
		params.FailureCode, params.FailureMessage = &code, &message
	}
	current.WorkDecision, current.OutcomeCode = &decision, params.OutcomeCode
	retryAt := cronAppTaskRetryAt(current, current.Status, params.Status, params.FinishedAt)
	var startedAtArg any
	if current.StartedAt != nil && !current.StartedAt.IsZero() {
		startedAtArg = *current.StartedAt
	}
	var completed AppTask
	if retryAt != nil {
		// The database transition guard only permits retrying a terminal failure
		// back to queued when its failure evidence is already durable. Record the
		// failed attempt first, then requeue it in the same transaction; neither
		// intermediate state is visible outside this transaction.
		tag, execErr := tx.Exec(ctx, `
			update app_tasks
			   set status = $3,
			       stdout_tail = $4,
			       stderr_tail = $5,
			       output_truncated = $6,
			       exit_code = $7,
			       failure_code = $8,
			       failure_message = $9,
			       finished_at = $10,
			       retry_at = null,
			       updated_at = $11,
			       started_at = $12,
			       work_decision = $13::jsonb, outcome_code = $14,
			       lease_token = null,
			       lease_owner = null,
			       lease_expires_at = null
			 where id = $1 and lease_token = $2`,
			params.ID, params.LeaseToken, string(params.Status), params.StdoutTail,
			params.StderrTail, params.OutputTruncated, params.ExitCode,
			params.FailureCode, params.FailureMessage, params.FinishedAt, params.FinishedAt, startedAtArg,
			policyJSON(&decision), params.OutcomeCode)
		if execErr != nil {
			return AppTask{}, mapErr(execErr)
		}
		if tag.RowsAffected() != 1 {
			return AppTask{}, ErrAppTaskLeaseLost
		}
		completed, err = scanAppTask(tx.QueryRow(ctx, `
			update app_tasks
			   set status = 'queued',
			       retry_at = $3,
			       updated_at = $4,
			       started_at = null,
			       finished_at = null
			 where id = $1 and status = $2
			returning `+appTaskSelectColumns,
			params.ID, string(params.Status), retryAt, params.FinishedAt))
	} else {
		completed, err = scanAppTask(tx.QueryRow(ctx, `
			update app_tasks
			   set status = $3,
			       stdout_tail = $4,
			       stderr_tail = $5,
			       output_truncated = $6,
			       exit_code = $7,
			       failure_code = $8,
			       failure_message = $9,
			       finished_at = $10,
			       retry_at = null,
			       updated_at = $11,
			       started_at = $12,
			       work_decision = $13::jsonb, outcome_code = $14,
			       lease_token = null,
			       lease_owner = null,
			       lease_expires_at = null
			 where id = $1 and lease_token = $2
			returning `+appTaskSelectColumns,
			params.ID, params.LeaseToken, string(params.Status), params.StdoutTail,
			params.StderrTail, params.OutputTruncated, params.ExitCode,
			params.FailureCode, params.FailureMessage, params.FinishedAt, params.FinishedAt, startedAtArg,
			policyJSON(&decision), params.OutcomeCode))
	}
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
	rows, err := tx.Query(ctx, `select `+appTaskSelectColumns+`
		  from app_tasks
		 where status in ('restoring', 'running')
		   and lease_expires_at <= $1
		 order by id
		 for update skip locked`, at)
	if err != nil {
		return AppTaskSweepResult{}, mapErr(err)
	}
	expiredTasks, err := scanAppTaskRows(rows)
	rows.Close()
	if err != nil {
		return AppTaskSweepResult{}, mapErr(err)
	}
	terminalReleaseTasks := make([]AppTask, 0)
	for _, task := range expiredTasks {
		terminal := false
		if task.CancelRequested != nil {
			_, err = tx.Exec(ctx, `update app_tasks
				set status = 'cancelled', retry_at = null, lease_token = null,
				    lease_owner = null, lease_expires_at = null, finished_at = $2, updated_at = $2
				where id = $1`, task.ID, at)
			result.Cancelled++
			terminal = true
		} else if task.Status == AppTaskRestoring {
			_, err = tx.Exec(ctx, `update app_tasks
				set status = 'queued', retry_at = null, lease_token = null,
				    lease_owner = null, lease_expires_at = null, finished_at = null, updated_at = $2
				where id = $1`, task.ID, at)
			result.RequeuedRestores++
		} else {
			var decision *workpolicy.Decision
			if task.FailureRules != nil {
				evaluated := workpolicy.Evaluate(task.FailureRules, workpolicy.Evidence{Uncertain: true})
				decision = &evaluated
			}
			message := "the app task worker lease expired after dispatch; the command was not replayed"
			if decision != nil {
				message = "completion receipt missing; the command outcome is uncertain"
			}
			_, err = tx.Exec(ctx, `update app_tasks
				set status = 'failed', retry_at = null, lease_token = null,
				    lease_owner = null, lease_expires_at = null, failure_code = 'lease_expired',
				    failure_message = $2, finished_at = $3, updated_at = $3,
				    work_decision = $4::jsonb
				where id = $1`, task.ID, message, at, policyJSON(decision))
			if err == nil && decision != nil && decision.Action == "retry" {
				retryTask := task
				retryTask.WorkDecision = decision
				retryAt := cronAppTaskRetryAt(retryTask, AppTaskRunning, AppTaskFailed, at)
				if retryAt != nil {
					_, err = tx.Exec(ctx, `update app_tasks
						set status = 'queued', retry_at = $2, started_at = null,
						    finished_at = null, updated_at = $3
						where id = $1 and status = 'failed'`, task.ID, retryAt, at)
				} else {
					result.FailedRuns++
					terminal = true
				}
			} else if err == nil {
				result.FailedRuns++
				terminal = true
			}
		}
		if err != nil {
			return AppTaskSweepResult{}, mapErr(err)
		}
		if task.Kind == AppTaskKindRelease && terminal {
			if task.CancelRequested != nil {
				task.Status = AppTaskCancelled
			} else {
				task.Status = AppTaskFailed
			}
			terminalReleaseTasks = append(terminalReleaseTasks, task)
		}
	}
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
       ` + alias + `.retry_max, ` + alias + `.retry_backoff_seconds, ` + alias + `.attempt_count, ` + alias + `.retry_at,
       ` + alias + `.lease_token, ` + alias + `.lease_owner, ` + alias + `.lease_expires_at, ` + alias + `.cancel_requested_at,
       ` + alias + `.stdout_tail, ` + alias + `.stderr_tail, ` + alias + `.output_truncated, ` + alias + `.exit_code,
       ` + alias + `.failure_code, ` + alias + `.failure_message, ` + alias + `.started_at, ` + alias + `.finished_at,
       ` + alias + `.created_at, ` + alias + `.updated_at, ` + alias + `.cron_id, ` + alias + `.scheduled_for,
       ` + alias + `.failure_rules, ` + alias + `.occurrence_id, ` + alias + `.start_deadline_at,
       ` + alias + `.work_decision, ` + alias + `.outcome_code,
       ` + alias + `.exclusive_operation_id, ` + alias + `.exclusive_generation`
}
