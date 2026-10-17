-- name: LockCIAccount :one
SELECT id FROM accounts WHERE id = $1 FOR UPDATE;

-- name: CountCIRevisions :one
SELECT count(*) FROM ci_pipeline_revisions WHERE account_id = $1;

-- name: InsertCIRevision :one
INSERT INTO ci_pipeline_revisions (id, account_id, name, digest, definition)
VALUES ($1, $2, $3, $4, $5) RETURNING *;

-- name: GetCIRevision :one
SELECT * FROM ci_pipeline_revisions WHERE account_id = $1 AND id = $2;

-- name: GetCIRevisionByDigest :one
SELECT * FROM ci_pipeline_revisions WHERE account_id = $1 AND name = $2 AND digest = $3;

-- name: ListCIRevisions :many
SELECT * FROM ci_pipeline_revisions WHERE account_id = $1
 AND (sqlc.arg(pipeline)::text = '' OR name = sqlc.arg(pipeline)::text)
ORDER BY created_at DESC, id LIMIT sqlc.arg(max_rows)::integer;

-- name: CountCIRuns :one
SELECT count(*) FROM ci_runs WHERE account_id = $1;

-- name: InsertCIRun :one
INSERT INTO ci_runs (id, account_id, revision_id, pipeline, commit_sha, idempotency_key, plan, status, blocked_reason)
VALUES ($1, $2, $3, $4, $5, $6, $7, 'blocked', 'compute_unavailable') RETURNING *;

-- name: GetCIRunByKey :one
SELECT * FROM ci_runs WHERE account_id = $1 AND idempotency_key = $2;

-- name: GetCIRun :one
SELECT * FROM ci_runs WHERE account_id = $1 AND id = $2;

-- name: LockCIRun :one
SELECT * FROM ci_runs WHERE account_id = $1 AND id = $2 FOR UPDATE;

-- name: ListCIRuns :many
SELECT * FROM ci_runs WHERE account_id = $1 ORDER BY created_at DESC, id LIMIT sqlc.arg(max_rows)::integer;

-- name: CancelCIRun :one
UPDATE ci_runs SET status = 'cancelled', blocked_reason = '', finished_at = now()
WHERE account_id = $1 AND id = $2 AND status = 'blocked' RETURNING *;

-- name: InsertCIEvent :exec
INSERT INTO ci_run_events (account_id, run_id, sequence, event_type, created_at) VALUES ($1, $2, $3, $4, $5);

-- name: ListCIEvents :many
SELECT * FROM ci_run_events WHERE account_id = $1 AND run_id = $2 ORDER BY sequence LIMIT sqlc.arg(max_rows)::integer;

-- name: CIExecutionClock :one
SELECT clock_timestamp()::timestamptz;

-- name: CountCIExecutions :one
SELECT count(*) FROM ci_executions WHERE account_id = $1 AND mode = 'qualification';

-- name: InsertCIExecution :one
INSERT INTO ci_executions (id, account_id, revision_id, commit_sha, idempotency_key, status, snapshot, created_at)
VALUES ($1, $2, $3, $4, $5, 'queued', $6, $7) RETURNING *;

-- name: GetCIExecutionByKey :one
SELECT * FROM ci_executions WHERE account_id = $1 AND idempotency_key = $2;

-- name: GetCIExecution :one
SELECT * FROM ci_executions WHERE account_id = $1 AND id = $2;

-- name: LockCIExecution :one
SELECT * FROM ci_executions WHERE account_id = $1 AND id = $2 FOR UPDATE;

-- name: UpdateCIExecution :one
UPDATE ci_executions SET status = $3, snapshot = $4, version = $5, fence = $6, lease_until = $7, finished_at = $8,
 next_reconcile_at = CASE WHEN ($4::jsonb->>'cancel_requested')='true' THEN least(next_reconcile_at,clock_timestamp()) ELSE next_reconcile_at END
WHERE account_id = $1 AND id = $2 RETURNING *;

-- name: ListCIGitHubBindings :many
SELECT * FROM ci_github_bindings WHERE account_id = $1 ORDER BY name LIMIT sqlc.arg(max_rows)::integer;

-- name: GetCIGitHubBinding :one
SELECT * FROM ci_github_bindings WHERE account_id = $1 AND id = $2;

-- name: UpsertCIGitHubBinding :one
INSERT INTO ci_github_bindings (id,account_id,name,revision_id,installation_id,repository_id,repository,push_branches,pr_branches,generation)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,1)
ON CONFLICT (account_id,name) DO UPDATE SET revision_id = EXCLUDED.revision_id,
 installation_id = EXCLUDED.installation_id, repository_id = EXCLUDED.repository_id, repository = EXCLUDED.repository,
 push_branches = EXCLUDED.push_branches, pr_branches = EXCLUDED.pr_branches, enabled = true,
 generation = ci_github_bindings.generation + 1 RETURNING *;

-- name: DisableCIGitHubBinding :one
UPDATE ci_github_bindings SET enabled = false, generation = generation + 1 WHERE account_id = $1 AND name = $2 RETURNING *;

-- name: IgnoreCIGitHubBindingTriggers :exec
UPDATE ci_github_triggers SET status = 'ignored', lease_token = NULL, lease_until = NULL, last_error = 'binding_disabled'
WHERE account_id = $1 AND binding_id = $2 AND status IN ('pending','processing');

-- name: IgnoreCIGitHubBindingChecks :exec
UPDATE ci_github_checks c SET status = 'ignored', lease_token = NULL, lease_until = NULL, last_error = 'binding_disabled'
WHERE c.account_id = $1 AND c.status IN ('pending','sending') AND c.run_id IN
 (SELECT t.run_id FROM ci_github_triggers t WHERE t.account_id = $1 AND t.binding_id = $2 AND t.run_id IS NOT NULL);

-- name: CountCIGitHubTriggers :one
SELECT (SELECT count(*) FROM ci_github_triggers t WHERE t.account_id = sqlc.arg(account_id)::uuid)
 + (SELECT count(*) FROM ci_automation_events e WHERE e.account_id = sqlc.arg(account_id)::uuid);

-- name: GetCIGitHubTriggerByEvent :one
SELECT id FROM ci_github_triggers WHERE account_id = $1 AND binding_id = $2 AND event_key = $3;

-- name: InsertCIGitHubTrigger :exec
INSERT INTO ci_github_triggers (id,account_id,binding_id,event_key,snapshot) VALUES ($1,$2,$3,$4,$5);

-- name: ExpireCIGitHubTriggers :exec
UPDATE ci_github_triggers SET status = 'dead', lease_token = NULL, lease_until = NULL
WHERE account_id = $1 AND attempts >= sqlc.arg(max_attempts)::integer AND status = 'processing' AND lease_until <= clock_timestamp();

-- name: ClaimCIGitHubTrigger :one
WITH candidate AS (
 SELECT w.id FROM ci_github_triggers w WHERE w.account_id = $1 AND w.attempts < sqlc.arg(max_attempts)::integer AND
 ((w.status = 'pending' AND w.next_attempt_at <= clock_timestamp()) OR (w.status = 'processing' AND w.lease_until <= clock_timestamp()))
 ORDER BY w.created_at,w.id FOR UPDATE SKIP LOCKED LIMIT 1
)
UPDATE ci_github_triggers t SET status = 'processing', attempts = attempts + 1, lease_token = sqlc.arg(token)::uuid,
 lease_until = clock_timestamp() + make_interval(secs => sqlc.arg(lease_seconds)::integer)
FROM candidate c WHERE t.id = c.id RETURNING t.*;

-- name: LockCIGitHubTrigger :one
SELECT * FROM ci_github_triggers WHERE account_id = $1 AND id = $2 FOR UPDATE;

-- name: AdmitCIGitHubTrigger :exec
UPDATE ci_github_triggers SET status = 'admitted', run_id = $3, lease_token = NULL, lease_until = NULL
WHERE account_id = $1 AND id = $2;

-- name: FailCIGitHubTrigger :execrows
UPDATE ci_github_triggers SET status = $4, last_error = $5, next_attempt_at = $6, lease_token = NULL, lease_until = NULL
WHERE account_id = $1 AND id = $2 AND lease_token = $3 AND status = 'processing' AND lease_until > clock_timestamp();

-- name: SetCIRunGitHubSource :one
UPDATE ci_runs SET github_source = $3 WHERE account_id = $1 AND id = $2 RETURNING *;

-- name: InsertCIGitHubCheck :exec
INSERT INTO ci_github_checks (account_id,run_id) VALUES ($1,$2);

-- name: ExpireCIGitHubChecks :exec
UPDATE ci_github_checks SET status = 'dead', lease_token = NULL, lease_until = NULL
WHERE account_id = $1 AND attempts >= sqlc.arg(max_attempts)::integer AND status = 'sending' AND lease_until <= clock_timestamp();

-- name: ClaimCIGitHubCheck :one
WITH candidate AS (
 SELECT run_id FROM ci_github_checks WHERE account_id = $1 AND attempts < sqlc.arg(max_attempts)::integer AND
 ((status = 'pending' AND next_attempt_at <= clock_timestamp()) OR (status = 'sending' AND lease_until <= clock_timestamp()))
 ORDER BY created_at,run_id FOR UPDATE SKIP LOCKED LIMIT 1
)
UPDATE ci_github_checks t SET status = 'sending', attempts = attempts + 1, lease_token = sqlc.arg(token)::uuid,
 lease_until = clock_timestamp() + make_interval(secs => sqlc.arg(lease_seconds)::integer)
FROM candidate c WHERE t.account_id = $1 AND t.run_id = c.run_id RETURNING t.*;

-- name: FinishCIGitHubCheck :execrows
UPDATE ci_github_checks SET status = $4, check_id = $5, last_error = $6, next_attempt_at = $7, lease_token = NULL, lease_until = NULL
WHERE account_id = $1 AND run_id = $2 AND lease_token = $3 AND status = 'sending' AND lease_until > clock_timestamp();

-- ADR-1573: apid-owned CI Jobs intent. Task claiming and instances remain schedd-owned.
-- name: CIJobsRunOwned :one
SELECT EXISTS (SELECT 1 FROM ci_job_attempts WHERE account_id = $1 AND run_id = $2);

-- name: CIJobsRetryBlocked :one
SELECT EXISTS (SELECT 1 FROM ci_job_attempts WHERE run_id = $1);

-- name: CIJobsAccount :one
SELECT plan FROM accounts WHERE id = $1 AND status = 'active' AND abuse_hold_at IS NULL;

-- name: LockCIJobsBinding :one
SELECT * FROM ci_github_bindings WHERE account_id = $1 AND id = $2 FOR SHARE;

-- name: LockCIJobsTemplate :one
SELECT * FROM jobs WHERE account_id = $1 AND id = $2 FOR SHARE;

-- name: CountCIJobsExecutions :one
SELECT count(*) FROM ci_executions WHERE account_id = $1 AND mode = 'jobs' AND archived_at IS NULL;

-- name: InsertCIJobsExecution :one
INSERT INTO ci_executions (id, account_id, revision_id, commit_sha, idempotency_key, mode, status, snapshot, created_at, rerun_parent_id, rerun_root_id)
VALUES ($1, $2, $3, $4, $5, 'jobs', 'queued', $6, $7, sqlc.narg(rerun_parent_id)::uuid, sqlc.narg(rerun_root_id)::uuid) RETURNING *;

-- name: EnsureCIJobsAttempt :exec
INSERT INTO ci_job_attempts (attempt_id, account_id, execution_id, job_name, fence)
VALUES ($1, $2, $3, $4, $5) ON CONFLICT (attempt_id) DO NOTHING;

-- name: LockCIJobsAttempt :one
SELECT * FROM ci_job_attempts WHERE account_id = $1 AND execution_id = $2 AND attempt_id = $3 FOR UPDATE;

-- name: FenceCIJobsAttempt :exec
UPDATE ci_job_attempts SET fence = $2 WHERE attempt_id = $1 AND fence <= $2;

-- name: DispatchCIJobsAttempt :exec
UPDATE ci_job_attempts SET run_id = $2, source = $3 WHERE attempt_id = $1;

-- name: CancelCIJobsAttempt :exec
UPDATE ci_job_attempts SET cancel_requested = true WHERE attempt_id = $1;

-- name: FinishCIJobsOutput :exec
UPDATE ci_job_attempts SET output = $2 WHERE attempt_id = $1 AND output IS NULL;

-- name: InsertCIJobsRun :exec
INSERT INTO job_runs (id,job_id,account_id,trigger_kind,tasks,parallelism,retry_max,task_timeout_s,command,
 image_ref_snapshot,image_resolved_digest_snapshot,image_storage_key_snapshot,ram_mb_snapshot,effective_env_snapshot,start_deadline_at)
VALUES ($1,$2,$3,'manual',1,1,0,$4,$5,$6,$7,$8,$9,'{}'::jsonb,$10);

-- name: InsertCIJobsTask :exec
INSERT INTO job_tasks (run_id,task_index) VALUES ($1,0);

-- name: ReadCIJobsTask :one
SELECT t.status, t.attempt, t.exit_code, (t.instance_id IS NOT NULL)::boolean AS had_instance,
 coalesce(t.status NOT IN ('queued','claimed') AND (t.instance_id IS NULL OR i.state = 'stopped'), false)::boolean AS stopped
FROM job_tasks t LEFT JOIN instances i ON i.id = t.instance_id
WHERE t.run_id = $1 AND t.task_index = 0;

-- name: CancelCIJobsTask :exec
UPDATE job_tasks SET status = 'cancelled', finished_at = coalesce(finished_at,clock_timestamp()),
 lease_token = NULL, lease_expires_at = NULL, last_lease_node = NULL
WHERE run_id = $1 AND status IN ('queued','claimed');

-- name: CancelCIJobsRun :exec
UPDATE job_runs SET aggregate_status = 'cancelled', finished_at = coalesce(finished_at,clock_timestamp()),
 tasks_succeeded = 0, tasks_failed = 0, tasks_cancelled = 1, tasks_running = 0
WHERE id = $1 AND aggregate_status IN ('queued','running')
 AND EXISTS (SELECT 1 FROM job_tasks WHERE run_id = $1 AND status = 'cancelled');

-- ADR-1574: task-bound runtime capabilities and sealed output.
-- name: CIJobsRuntimeOwner :one
SELECT account_id, execution_id FROM ci_job_attempts WHERE run_id = $1;

-- name: CIJobsLaunchSnapshot :one
SELECT * FROM job_runs WHERE id=$1 FOR SHARE;

-- name: CIJobsRuntimeTask :one
SELECT t.status,t.attempt,t.instance_id,t.lease_token,t.lease_expires_at,t.finished_at,i.state AS instance_state,
 coalesce(i.kind='job_task' AND i.job_id=r.job_id AND i.ram_mb=r.ram_mb_snapshot,false)::boolean AS bound_instance
FROM job_tasks t JOIN job_runs r ON r.id=t.run_id LEFT JOIN instances i ON i.id=t.instance_id WHERE t.run_id=$1 AND t.task_index=0;

-- name: ReadCIJobsRuntime :one
SELECT * FROM ci_job_runtime WHERE attempt_id=$1;

-- name: BeginCIJobsRuntimeTransfer :exec
UPDATE ci_job_runtime SET transfer_token=$2,transfer_until=$3 WHERE attempt_id=$1;

-- name: EndCIJobsRuntimeTransfer :exec
UPDATE ci_job_runtime SET transfer_token=NULL,transfer_until=NULL WHERE attempt_id=$1 AND instance_id=$2 AND transfer_token=$3;

-- name: InsertCIJobsRuntime :exec
INSERT INTO ci_job_runtime(attempt_id,instance_id,capability_hash,expires_at,created_at)
VALUES($1,$2,$3,$4,$5) ON CONFLICT(attempt_id) DO NOTHING;

-- name: CancelCIJobsRuntime :exec
UPDATE ci_job_runtime SET cancelled_at=coalesce(cancelled_at,$2) WHERE attempt_id=$1;

-- name: CancelCIJobsExecutionRuntime :exec
UPDATE ci_job_runtime SET cancelled_at=coalesce(cancelled_at,$2)
WHERE attempt_id IN (SELECT attempt_id FROM ci_job_attempts WHERE execution_id=$1);

-- name: ListCIJobsRuntimeObjects :many
SELECT * FROM ci_job_runtime_objects WHERE attempt_id=$1 ORDER BY name;

-- name: ReserveCIJobsRuntimeObject :exec
INSERT INTO ci_job_runtime_objects(attempt_id,name,digest,bytes) VALUES($1,$2,$3,$4);

-- name: RetainCIJobsRuntimeObject :exec
UPDATE ci_job_runtime_objects SET retained=true WHERE attempt_id=$1 AND name=$2;

-- name: SealCIJobsRuntime :exec
UPDATE ci_job_runtime SET result=$2,sealed_at=$3 WHERE attempt_id=$1 AND result IS NULL;

-- name: ReadSealedCIJobsResult :one
SELECT r.result FROM ci_job_runtime r JOIN ci_job_attempts a ON a.attempt_id=r.attempt_id
WHERE a.account_id=$1 AND a.execution_id=$2 AND a.attempt_id=$3 AND r.result IS NOT NULL;
