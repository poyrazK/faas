-- name: LockCIStorageBudget :one
SELECT lock_id FROM ci_storage_budget WHERE lock_id=true FOR UPDATE;

-- name: CIStorageStats :one
SELECT count(*)::bigint AS journal_total,
 count(*) FILTER(WHERE account_id=$1)::bigint AS journal_account,
 count(*) FILTER(WHERE state!='deleted')::bigint AS objects_total,
 count(*) FILTER(WHERE state!='deleted' AND account_id=$1)::bigint AS objects_account,
 coalesce(sum(bytes) FILTER(WHERE state!='deleted'),0)::bigint AS bytes_total,
 coalesce(sum(bytes) FILTER(WHERE state!='deleted' AND account_id=$1),0)::bigint AS bytes_account
FROM ci_managed_objects;

-- name: CIStorageActivity :one
SELECT count(*)::bigint AS total,
 count(*) FILTER(WHERE account_id=$1)::bigint AS account
FROM ci_managed_objects WHERE op_until>sqlc.arg(now)::timestamptz;

-- name: LockCIManagedObject :one
SELECT * FROM ci_managed_objects WHERE object_key=$1 FOR UPDATE;

-- name: ReadCIManagedObject :one
SELECT * FROM ci_managed_objects WHERE object_key=$1;

-- name: ReadCICachedSource :one
SELECT * FROM ci_managed_objects WHERE attempt_id=$1 AND kind='source' ORDER BY created_at LIMIT 1;

-- name: CIStorageAttempt :one
SELECT * FROM ci_job_attempts WHERE account_id=$1 AND execution_id=$2 AND attempt_id=$3;

-- name: InsertCIManagedObject :exec
INSERT INTO ci_managed_objects(object_key,id,account_id,execution_id,attempt_id,job_id,kind,digest,bytes,state,created_at,expires_at)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,'pending',$10,$11);

-- name: BeginCIStorageOperation :exec
UPDATE ci_managed_objects SET state=$2,op_token=$3,op_until=$4 WHERE object_key=$1;

-- name: ReleaseCIStorageOperation :exec
UPDATE ci_managed_objects SET op_token=NULL,op_until=NULL WHERE object_key=$1 AND op_token=$2;

-- name: CommitCIStorageWrite :execrows
UPDATE ci_managed_objects SET state='ready',ready_at=$3,expires_at=$4,op_token=NULL,op_until=NULL
WHERE object_key=$1 AND op_token=$2 AND state='pending' AND op_until>$3;

-- name: CommitCIStorageRetirement :execrows
UPDATE ci_managed_objects SET state='deleted',deleted_at=$3,op_token=NULL,op_until=NULL
WHERE object_key=$1 AND op_token=$2 AND state='deleting' AND op_until>$3;

-- name: ListCIStorageCandidates :many
SELECT object_key FROM ci_managed_objects
WHERE (op_until IS NULL OR op_until<=sqlc.arg(now)::timestamptz)
 AND ((state='ready' AND expires_at<=sqlc.arg(now)::timestamptz)
  OR (state='pending' AND created_at<=sqlc.arg(orphan_before)::timestamptz)
  OR state='deleting')
 AND NOT EXISTS(SELECT 1 FROM ci_executions e WHERE e.id=ci_managed_objects.execution_id
  AND (e.status NOT IN ('succeeded','failed','cancelled') OR e.lease_until>sqlc.arg(now)::timestamptz))
 AND (EXISTS(SELECT 1 FROM ci_job_attempts a WHERE a.attempt_id=ci_managed_objects.attempt_id
  AND (a.run_id IS NULL OR (a.output IS NOT NULL AND EXISTS(
   SELECT 1 FROM job_tasks t LEFT JOIN instances i ON i.id=t.instance_id WHERE t.run_id=a.run_id
    AND t.task_index=0 AND t.attempt=1 AND t.status NOT IN ('queued','claimed')
    AND (t.instance_id IS NULL OR i.state='stopped')))))
  OR (NOT EXISTS(SELECT 1 FROM ci_job_attempts a WHERE a.attempt_id=ci_managed_objects.attempt_id)
   AND NOT EXISTS(SELECT 1 FROM instances i WHERE i.job_id=ci_managed_objects.job_id AND i.state!='stopped')))
ORDER BY created_at,object_key LIMIT sqlc.arg(batch)::integer;

-- name: CIStorageRetirementSafe :one
SELECT NOT EXISTS(SELECT 1 FROM ci_executions e WHERE e.id=$1 AND e.account_id=$2
 AND (e.status NOT IN ('succeeded','failed','cancelled') OR e.lease_until>sqlc.arg(now)::timestamptz))
 AND (
 EXISTS(SELECT 1 FROM ci_job_attempts a WHERE a.attempt_id=$3 AND a.account_id=$2 AND a.execution_id=$1
  AND (a.run_id IS NULL OR (a.output IS NOT NULL AND EXISTS(
   SELECT 1 FROM job_tasks t LEFT JOIN instances i ON i.id=t.instance_id
   WHERE t.run_id=a.run_id AND t.task_index=0 AND t.attempt=1
    AND t.status NOT IN ('queued','claimed') AND (t.instance_id IS NULL OR i.state='stopped')))))
 OR (NOT EXISTS(SELECT 1 FROM ci_job_attempts a WHERE a.attempt_id=$3)
  AND NOT EXISTS(SELECT 1 FROM instances i WHERE i.job_id=$4 AND i.state!='stopped'))
 ) AS safe;

-- name: RetireCIStorageRuntime :exec
DELETE FROM ci_job_runtime r USING ci_job_attempts a,ci_executions e,instances i
WHERE r.attempt_id=$1 AND a.attempt_id=r.attempt_id AND e.id=a.execution_id
 AND a.output IS NOT NULL AND e.status IN ('succeeded','failed','cancelled')
 AND (e.lease_until IS NULL OR e.lease_until<=sqlc.arg(now)::timestamptz)
 AND i.id=r.instance_id AND i.state='stopped'
 AND (r.transfer_until IS NULL OR r.transfer_until<=sqlc.arg(now)::timestamptz)
 AND NOT EXISTS(SELECT 1 FROM ci_managed_objects o WHERE o.attempt_id=r.attempt_id AND o.state!='deleted');

-- name: CIStorageAccountActive :one
SELECT EXISTS(SELECT 1 FROM accounts WHERE id=$1 AND status='active' AND abuse_hold_at IS NULL);
