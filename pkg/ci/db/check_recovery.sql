-- name: ListCICheckDeliveries :many
SELECT e.id AS execution_id,coalesce(e.snapshot->>'pipeline','')::text AS pipeline,e.commit_sha,e.status AS execution_status,
 e.version AS desired_version,coalesce(c.status,'not_requested')::text AS delivery_status,
 coalesce(c.attempts,0)::integer AS attempts,coalesce(c.last_error,'')::text AS last_error,
 coalesce(c.check_id,0)::bigint AS check_id,coalesce(c.delivered_version,0)::bigint AS delivered_version,
 coalesce(c.retry_generation,0)::integer AS retry_generation,c.next_attempt_at,c.lease_until,
 r.id AS retry_id,coalesce(r.generation,0)::integer AS requested_generation,r.requested_at,r.applied_at
FROM ci_executions e LEFT JOIN ci_execution_checks c ON c.execution_id=e.id AND c.account_id=e.account_id
LEFT JOIN LATERAL (SELECT id,generation,requested_at,applied_at FROM ci_execution_check_retries
 WHERE account_id=e.account_id AND execution_id=e.id ORDER BY generation DESC LIMIT 1) r ON true
WHERE e.account_id=$1 AND e.mode='jobs' AND e.archived_at IS NULL ORDER BY e.created_at DESC,e.id LIMIT sqlc.arg(max_rows)::integer;

-- name: GetCICheckRetryByKey :one
SELECT * FROM ci_execution_check_retries WHERE account_id=$1 AND key_digest=$2;

-- apid never waits for a publisher's network transaction.
-- name: TryLockCICheckDelivery :one
SELECT * FROM ci_execution_checks WHERE account_id=$1 AND execution_id=$2 FOR UPDATE SKIP LOCKED;

-- name: GetCICheckRetryByGeneration :one
SELECT * FROM ci_execution_check_retries WHERE account_id=$1 AND execution_id=$2 AND generation=$3;

-- name: InsertCICheckRetry :one
INSERT INTO ci_execution_check_retries(id,account_id,execution_id,generation,key_digest)
VALUES($1,$2,$3,$4,$5) RETURNING *;

-- githubd atomically consumes retry intent and resets its own delivery budget.
-- Existing Check identity, delivered version/digest and execution state survive.
-- name: ApplyCICheckRetry :exec
WITH candidate AS (
 SELECT c.execution_id,r.id,r.generation FROM ci_execution_checks c JOIN ci_execution_check_retries r
 ON r.account_id=c.account_id AND r.execution_id=c.execution_id AND r.generation=c.retry_generation+1
 WHERE c.account_id=$1 AND c.status='dead' AND c.attempts>=sqlc.arg(max_attempts)::integer
 AND c.last_error IN ('publish_failed','unavailable','attempts_exhausted') AND r.applied_at IS NULL
 ORDER BY r.requested_at,r.id FOR UPDATE OF c,r SKIP LOCKED LIMIT 1
), reset AS (
 UPDATE ci_execution_checks c SET status='pending',attempts=0,last_error='',lease_token=NULL,lease_until=NULL,
 next_attempt_at=clock_timestamp(),retry_generation=x.generation FROM candidate x
 WHERE c.execution_id=x.execution_id RETURNING x.id
)
UPDATE ci_execution_check_retries r SET applied_at=clock_timestamp() FROM reset x WHERE r.id=x.id;
