-- name: GetCIExecutionArchive :one
SELECT * FROM ci_execution_archives WHERE account_id=$1 AND execution_id=$2;

-- name: ListCIExecutionArchives :many
SELECT * FROM ci_execution_archives WHERE account_id=$1 ORDER BY archived_at DESC,execution_id LIMIT sqlc.arg(max_rows)::integer;

-- name: CountCIExecutionArchives :one
SELECT count(*) FROM ci_execution_archives WHERE account_id=$1;

-- name: InsertCIExecutionArchive :exec
INSERT INTO ci_execution_archives(account_id,execution_id,receipt,archived_at) VALUES($1,$2,$3,$4);

-- Only terminal detail is removed. Identity columns and version stay unchanged.
-- name: CompactCIExecution :execrows
UPDATE ci_executions SET archived_at=$3,snapshot=jsonb_build_object('mode','jobs','status',status,'compute','{}'::jsonb)
 || CASE WHEN rerun_parent_id IS NULL THEN '{}'::jsonb ELSE jsonb_build_object('rerun',snapshot->'rerun') END
WHERE account_id=$1 AND id=$2 AND mode='jobs' AND archived_at IS NULL
 AND status IN ('succeeded','failed','cancelled') AND lease_until IS NULL;

-- Retain no-retry fences and Jobs/instance receipts, but remove CI output bytes.
-- name: CompactCIArchiveAttempts :exec
UPDATE ci_job_attempts SET source=CASE WHEN run_id IS NULL THEN NULL ELSE '{}'::jsonb END,output=NULL,progress=NULL
WHERE account_id=$1 AND execution_id=$2;

-- name: DeleteCIArchiveRuntime :exec
DELETE FROM ci_job_runtime WHERE attempt_id IN
 (SELECT attempt_id FROM ci_job_attempts WHERE account_id=$1 AND execution_id=$2);

-- name: CIArchiveHasCheck :one
SELECT EXISTS(SELECT 1 FROM ci_execution_checks WHERE account_id=$1 AND execution_id=$2);

-- name: CIArchiveRetries :many
SELECT * FROM ci_execution_check_retries WHERE account_id=$1 AND execution_id=$2 ORDER BY generation LIMIT sqlc.arg(max_rows)::integer;

-- Caller holds storage-budget -> account -> execution locks. No new transfer,
-- storage reservation or Jobs start can race these retirement/stop checks.
-- name: CIArchiveResourcesSafe :one
SELECT NOT EXISTS(SELECT 1 FROM ci_managed_objects o WHERE o.account_id=$1 AND o.execution_id=$2
 AND (o.state<>'deleted' OR o.op_until>clock_timestamp()))
 AND NOT EXISTS(SELECT 1 FROM ci_job_attempts a LEFT JOIN job_tasks t ON t.run_id=a.attempt_id AND t.task_index=0
 LEFT JOIN instances i ON i.id=t.instance_id WHERE a.account_id=$1 AND a.execution_id=$2
 AND ((a.run_id IS NULL AND t.run_id IS NOT NULL) OR (a.run_id IS NOT NULL
 AND (t.run_id IS NULL OR t.attempt<>1 OR t.status NOT IN ('succeeded','failed','cancelled')
 OR (t.instance_id IS NOT NULL AND (i.id IS NULL OR i.state<>'stopped' OR a.output IS NULL))))))
 AND NOT EXISTS(SELECT 1 FROM ci_job_runtime r JOIN ci_job_attempts a ON a.attempt_id=r.attempt_id
 LEFT JOIN instances i ON i.id=r.instance_id WHERE a.account_id=$1 AND a.execution_id=$2
 AND (i.id IS NULL OR i.state<>'stopped' OR r.transfer_until>clock_timestamp())) AS safe;
