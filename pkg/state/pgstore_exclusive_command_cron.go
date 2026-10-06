package state

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/exclusivework"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ExclusiveCommandCronTaskStore = (*PgStore)(nil)
var _ ExclusiveCommandCronFireNowStore = (*PgStore)(nil)

// AdmitExclusiveCommandCronFireNow couples the operation admission and manual
// request receipt in one PostgreSQL transaction.
func (s *PgStore) AdmitExclusiveCommandCronFireNow(ctx context.Context, requestID string, firedAt time.Time, admission ExclusiveAdmission) (ExclusiveOperation, bool, error) {
	if requestID == "" || firedAt.IsZero() {
		return ExclusiveOperation{}, false, ErrAppTaskInvalid
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return ExclusiveOperation{}, false, fmt.Errorf("state: begin exclusive command fire-now admission: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var cronID, accountID, requestStatus string
	if err := tx.QueryRow(ctx, `select cron_id::text,account_id::text,status from cron_fire_now_requests
		where id=$1::uuid for update`, requestID).Scan(&cronID, &accountID, &requestStatus); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ExclusiveOperation{}, false, ErrFireNowRequestNotFound
		}
		return ExclusiveOperation{}, false, fmt.Errorf("state: lock exclusive command fire-now request: %w", err)
	}
	if requestStatus != string(FireNowStatusRunning) || admission.AccountID != accountID {
		return ExclusiveOperation{}, false, ErrFireNowRequestNotFound
	}
	var appID string
	if err := tx.QueryRow(ctx, `select app_id::text from crons where id=$1::uuid`, cronID).Scan(&appID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ExclusiveOperation{}, false, ErrNotFound
		}
		return ExclusiveOperation{}, false, err
	}
	if err := validateExclusiveCommandCronAdmission(admission, accountID, appID, cronID); err != nil {
		return ExclusiveOperation{}, false, ErrInvalidArgument
	}
	ownerTx := &exclusivePostgresTx{ctx: ctx, db: tx, q: sqlc.New()}
	operation, joined, err := admitExclusiveTransaction(ownerTx, admission)
	if err != nil {
		return ExclusiveOperation{}, false, err
	}
	var cronAccountID, cronAppID string
	var command []string
	var enabled, skipIfRunning bool
	var suspendedReason string
	err = tx.QueryRow(ctx, `select a.account_id::text,c.app_id::text,c.command,c.enabled,c.skip_if_running,c.suspended_reason
		from crons c join apps a on a.id=c.app_id where c.id=$1::uuid and a.status<>'deleted'
		for update of c`, cronID).Scan(&cronAccountID, &cronAppID, &command, &enabled, &skipIfRunning, &suspendedReason)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && (cronAccountID != accountID || cronAppID != admission.AppID)) {
		return ExclusiveOperation{}, false, ErrNotFound
	}
	if err != nil {
		return ExclusiveOperation{}, false, fmt.Errorf("state: lock exclusive command cron: %w", err)
	}
	if !enabled {
		return ExclusiveOperation{}, false, ErrAppTaskCronDisabled
	}
	if suspendedReason != "" {
		return ExclusiveOperation{}, false, ErrAppTaskCronSuspended
	}
	if len(command) == 0 {
		return ExclusiveOperation{}, false, ErrAppTaskInvalid
	}
	if skipIfRunning {
		var active bool
		if err := tx.QueryRow(ctx, `select exists(select 1 from app_tasks where cron_id=$1::uuid
			and status in ('queued','restoring','running') and exclusive_operation_id is null)`, cronID).Scan(&active); err != nil {
			return ExclusiveOperation{}, false, fmt.Errorf("state: check exclusive command cron overlap: %w", err)
		}
		if active {
			return ExclusiveOperation{}, false, ErrAppTaskCronOverlap
		}
	}
	var deploymentAvailable bool
	if err := tx.QueryRow(ctx, `select exists(select 1 from deployments where app_id=$1::uuid and status='live'
		and rootfs_key is not null and rootfs_key<>'' and image_digest<>'')`, appID).Scan(&deploymentAvailable); err != nil {
		return ExclusiveOperation{}, false, fmt.Errorf("state: check exclusive command cron deployment: %w", err)
	}
	if !deploymentAvailable {
		return ExclusiveOperation{}, false, ErrAppTaskDeploymentUnavailable
	}
	tag, err := tx.Exec(ctx, `update cron_fire_now_requests set status='succeeded',operation_id=$2::uuid,
		invocation_id=null,finished_at=clock_timestamp() where id=$1::uuid and status='running'`, requestID, operation.ID)
	if err != nil {
		return ExclusiveOperation{}, false, fmt.Errorf("state: complete exclusive command fire-now receipt: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return ExclusiveOperation{}, false, ErrFireNowRequestNotFound
	}
	if err := tx.Commit(ctx); err != nil {
		return ExclusiveOperation{}, false, fmt.Errorf("state: commit exclusive command fire-now admission: %w", err)
	}
	return operation, joined, nil
}

// CreateExclusiveCommandCronAppTask materializes a command-cron AppTask under
// the operation row lock. A stale generation cannot create an executable task,
// and the occurrence or fire-now receipt is linked in the same transaction.
func (s *PgStore) CreateExclusiveCommandCronAppTask(ctx context.Context, accountID, appID, operationID string, generation int64, expectedCronID string, createdAt time.Time) (AppTask, error) {
	if accountID == "" || appID == "" || operationID == "" || expectedCronID == "" || generation <= 0 || createdAt.IsZero() {
		return AppTask{}, ErrAppTaskInvalid
	}
	createdAt = createdAt.UTC()
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return AppTask{}, fmt.Errorf("state: begin exclusive command cron task: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var current bool
	err = tx.QueryRow(ctx, `select true from exclusive_work_operations
		where id=$1::uuid and account_id=$2::uuid and app_id=$3::uuid
		  and state='running' and generation=$4
		  and lease_expires_at > clock_timestamp() and attempt_deadline > clock_timestamp()
		for update`, operationID, accountID, appID, generation).Scan(&current)
	if errors.Is(err, pgx.ErrNoRows) {
		return AppTask{}, exclusivework.ErrStaleOwner
	}
	if err != nil {
		return AppTask{}, fmt.Errorf("state: validate exclusive command cron owner: %w", err)
	}
	if !current {
		return AppTask{}, exclusivework.ErrStaleOwner
	}

	task, err := scanAppTask(tx.QueryRow(ctx, `select `+appTaskSelectColumns+`
		from app_tasks where exclusive_operation_id=$1::uuid and exclusive_generation=$2
		order by created_at,id limit 1 for update`, operationID, generation))
	if err == nil {
		if _, err := tx.Exec(ctx, `update cron_fire_now_requests set task_id=$2::uuid
			where operation_id=$1::uuid and task_id is null`, operationID, task.ID); err != nil {
			return AppTask{}, fmt.Errorf("state: link exclusive fire-now task receipt: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return AppTask{}, fmt.Errorf("state: commit existing exclusive command cron task: %w", err)
		}
		return task, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return AppTask{}, fmt.Errorf("state: load existing exclusive command cron task: %w", err)
	}

	var occurrenceID, cronID string
	var scheduledFor, deadline pgtype.Timestamptz
	err = tx.QueryRow(ctx, `select id::text, cron_id::text, scheduled_for, start_deadline_at
		from schedule_occurrences
		where exclusive_operation_id=$1::uuid and cron_id is not null and status <> 'coalesced'
		order by created_at,id limit 1 for update`, operationID).Scan(&occurrenceID, &cronID, &scheduledFor, &deadline)
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(ctx, `select cron_id::text from cron_fire_now_requests
			where operation_id=$1::uuid order by requested_at,id limit 1 for update`, operationID).Scan(&cronID)
		if errors.Is(err, pgx.ErrNoRows) {
			return AppTask{}, ErrNotFound
		}
	}
	if err != nil {
		return AppTask{}, fmt.Errorf("state: resolve exclusive command cron trigger: %w", err)
	}
	if cronID != expectedCronID {
		return AppTask{}, ErrNotFound
	}

	var cronAccountID, resolvedAppID, deploymentID, scope, artifactKey, imageDigest string
	var command []string
	var commandShell, enabled bool
	var suspendedReason string
	var timeoutSeconds, maxOutputBytes, retryMax, retryBackoffSeconds int
	var failureRulesRaw []byte
	err = tx.QueryRow(ctx, `select a.account_id::text,c.app_id::text,c.command,c.command_shell,
		c.command_timeout_seconds,c.command_max_output_bytes,c.retry_max,c.retry_backoff_seconds,
		c.enabled,c.suspended_reason,c.failure_rules
		from crons c join apps a on a.id=c.app_id
		where c.id=$1::uuid and a.status <> 'deleted' for update of c`, cronID).Scan(
		&cronAccountID, &resolvedAppID, &command, &commandShell, &timeoutSeconds, &maxOutputBytes,
		&retryMax, &retryBackoffSeconds, &enabled, &suspendedReason, &failureRulesRaw)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && (cronAccountID != accountID || resolvedAppID != appID)) {
		return AppTask{}, ErrNotFound
	}
	if err != nil {
		return AppTask{}, fmt.Errorf("state: load exclusive command cron: %w", err)
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
	var activeCronTask bool
	if err := tx.QueryRow(ctx, `select exists(select 1 from app_tasks where cron_id=$1::uuid
		and status in ('queued','restoring','running') and exclusive_operation_id is null)`, cronID).Scan(&activeCronTask); err != nil {
		return AppTask{}, fmt.Errorf("state: check exclusive command cron overlap: %w", err)
	}
	if activeCronTask {
		return AppTask{}, ErrAppTaskCronOverlap
	}
	if err := tx.QueryRow(ctx, `select id::text,coalesce(nullif(scope,''),'default'),rootfs_key,image_digest
		from deployments where app_id=$1::uuid and status='live'
		  and rootfs_key is not null and rootfs_key<>'' and image_digest<>''
		order by (traffic_percent > 0) desc,created_at desc,id desc limit 1 for update`, appID).
		Scan(&deploymentID, &scope, &artifactKey, &imageDigest); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return AppTask{}, ErrAppTaskDeploymentUnavailable
		}
		return AppTask{}, fmt.Errorf("state: select live deployment for exclusive command cron: %w", err)
	}
	var scheduledForArg, occurrenceArg, deadlineArg any
	if occurrenceID != "" {
		scheduledForArg, occurrenceArg = scheduledFor.Time, occurrenceID
		if deadline.Valid {
			deadlineArg = deadline.Time
		}
	}
	task, err = scanAppTask(tx.QueryRow(ctx, `insert into app_tasks (
		account_id,app_id,deployment_id,kind,command,command_shell,deployment_scope,artifact_key,image_digest,
		timeout_seconds,max_output_bytes,retry_max,retry_backoff_seconds,created_at,updated_at,cron_id,scheduled_for,
		failure_rules,occurrence_id,start_deadline_at,exclusive_operation_id,exclusive_generation)
		values ($1::uuid,$2::uuid,$3::uuid,'cron',$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$13,$14::uuid,$15,
		$16::jsonb,$17::uuid,$18,$19::uuid,$20)
		returning `+appTaskSelectColumns,
		accountID, appID, deploymentID, command, commandShell, scope, artifactKey, imageDigest,
		timeoutSeconds, maxOutputBytes, retryMax, retryBackoffSeconds, createdAt, cronID, scheduledForArg,
		failureRulesRaw, occurrenceArg, deadlineArg, operationID, generation))
	if err != nil {
		return AppTask{}, fmt.Errorf("state: create exclusive command cron task: %w", mapErr(err))
	}
	if occurrenceID != "" {
		if _, err := tx.Exec(ctx, `update schedule_occurrences set app_task_id=$2::uuid,status='queued',reason='',finished_at=null,updated_at=$3
			where id=$1::uuid`, occurrenceID, task.ID, createdAt); err != nil {
			return AppTask{}, fmt.Errorf("state: link exclusive command cron occurrence: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `update cron_fire_now_requests set task_id=$2::uuid
		where operation_id=$1::uuid and task_id is null`, operationID, task.ID); err != nil {
		return AppTask{}, fmt.Errorf("state: link exclusive fire-now task receipt: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return AppTask{}, fmt.Errorf("state: commit exclusive command cron task: %w", err)
	}
	return task, nil
}
