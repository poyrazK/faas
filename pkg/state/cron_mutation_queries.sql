-- name: LockCronMutationApp :one
SELECT id::text FROM apps WHERE id=sqlc.arg(app_id)::text::uuid
 AND account_id=sqlc.arg(account_id)::text::uuid AND status <> 'deleted' FOR UPDATE;

-- name: ReadCronMutationSnapshot :one
-- Keep this projection identical to CronReviewToken's state.Cron input. A
-- scheduler last_fired_at stamp is deliberately absent. The row lock also
-- fences FK inserts during the final reference check and delete.
SELECT jsonb_build_object(
 'ID', id, 'AppID', app_id, 'Schedule', schedule, 'Path', path,
 'Enabled', enabled, 'SuspendedReason', suspended_reason,
 'Timezone', timezone, 'SkipIfRunning', skip_if_running, 'CreatedAt', created_at,
 'Command', command, 'CommandShell', command_shell,
 'CommandTimeoutSeconds', command_timeout_seconds, 'CommandMaxOutputBytes', command_max_output_bytes,
 'RetryMax', retry_max, 'RetryBackoffSeconds', retry_backoff_seconds,
 'SchedulePolicy', schedule_policy, 'FailureRules', failure_rules,
 'ScheduleRevision', schedule_revision, 'ScheduleAnchorAt', coalesce(schedule_anchor_at,'0001-01-01T00:00:00Z'::timestamptz),
 'DeploymentScope', deployment_scope, 'ClonePaused', clone_paused
)::jsonb AS snapshot
FROM crons WHERE id=sqlc.arg(cron_id)::text::uuid AND app_id=sqlc.arg(app_id)::text::uuid
 AND suspended_reason <> 'app_deleted' FOR UPDATE;

-- name: ReadCronMutationRecord :one
SELECT record FROM cron_mutation_receipts
WHERE app_id=sqlc.arg(app_id)::text::uuid AND account_id=sqlc.arg(account_id)::text::uuid
 AND request_id=sqlc.arg(request_id)::text::uuid AND retained_until > now();

-- name: WriteCronMutationRecord :exec
INSERT INTO cron_mutation_receipts(app_id,account_id,request_id,cron_id,status,record,retained_until)
VALUES(sqlc.arg(app_id)::text::uuid,sqlc.arg(account_id)::text::uuid,sqlc.arg(request_id)::text::uuid,
 sqlc.arg(cron_id)::text::uuid,sqlc.arg(status),sqlc.arg(record)::jsonb,sqlc.arg(retained_until)::timestamptz)
ON CONFLICT(app_id,request_id) DO UPDATE SET status=EXCLUDED.status,record=EXCLUDED.record;

-- name: SupersedeCronMutations :exec
UPDATE cron_mutation_receipts SET status='conflict',
 record=jsonb_set(jsonb_set(record,'{receipt,status}','"conflict"'),'{receipt,updated_at}',to_jsonb(now()))
WHERE app_id=sqlc.arg(app_id)::text::uuid AND cron_id=sqlc.arg(cron_id)::text::uuid
 AND status IN ('accepted','partial');

-- name: PruneCronMutations :exec
DELETE FROM cron_mutation_receipts WHERE ctid IN (
 SELECT ctid FROM cron_mutation_receipts WHERE app_id=sqlc.arg(app_id)::text::uuid AND retained_until <= now()
 LIMIT sqlc.arg(batch_limit)::int);

-- name: CountCronMutations :one
SELECT count(*) FROM cron_mutation_receipts WHERE app_id=sqlc.arg(app_id)::text::uuid;

-- name: UpdateReviewedCron :execrows
UPDATE crons SET schedule=coalesce(sqlc.narg(schedule)::text,schedule),
 path=coalesce(sqlc.narg(path)::text,path),enabled=coalesce(sqlc.narg(enabled)::boolean,enabled),
 timezone=coalesce(sqlc.narg(timezone)::text,timezone)
WHERE id=sqlc.arg(cron_id)::text::uuid AND app_id=sqlc.arg(app_id)::text::uuid;

-- name: DetachReviewedCronInvocations :execrows
UPDATE invocations SET cron_id=NULL WHERE id IN (
 SELECT id FROM invocations WHERE cron_id=sqlc.arg(cron_id)::text::uuid AND state <> 'pending' ORDER BY id LIMIT sqlc.arg(batch_limit)::int);

-- name: DetachReviewedCronTasks :execrows
UPDATE app_tasks SET cron_id=NULL WHERE id IN (
 SELECT id FROM app_tasks WHERE cron_id=sqlc.arg(cron_id)::text::uuid ORDER BY id LIMIT sqlc.arg(batch_limit)::int);

-- name: ReviewedCronReferencesRemain :one
SELECT EXISTS(SELECT 1 FROM invocations WHERE cron_id=sqlc.arg(cron_id)::text::uuid)
 OR EXISTS(SELECT 1 FROM app_tasks WHERE cron_id=sqlc.arg(cron_id)::text::uuid) AS present;
