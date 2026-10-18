-- name: ListCISourceCheckDeliveries :many
SELECT e.id AS event_id,e.binding_id,coalesce(c.snapshot->>'Pipeline',p.name,'')::text AS pipeline,
 coalesce(e.snapshot->'source'->>'commit_sha','')::text AS commit_sha,e.status AS source_status,
 coalesce(c.status,'not_requested')::text AS delivery_status,coalesce(c.attempts,0)::integer AS attempts,
 coalesce(c.last_error,'')::text AS last_error,coalesce(c.check_id,0)::bigint AS check_id,
 coalesce(c.retry_generation,0)::integer AS retry_generation,c.next_attempt_at,c.lease_until,
 r.id AS retry_id,coalesce(r.generation,0)::integer AS requested_generation,r.requested_at,r.applied_at
FROM ci_automation_events e
LEFT JOIN ci_automation_path_checks c ON c.account_id=e.account_id AND c.event_id=e.id
LEFT JOIN ci_pipeline_revisions p ON p.account_id=e.account_id AND p.id=(e.snapshot->'binding'->>'revision_id')::uuid
LEFT JOIN LATERAL (SELECT id,generation,requested_at,applied_at FROM ci_source_check_retries
 WHERE account_id=e.account_id AND event_id=e.id ORDER BY generation DESC LIMIT 1) r ON true
WHERE e.account_id=$1 AND e.binding_id=$2 AND (sqlc.narg(event_id)::uuid IS NULL OR e.id=sqlc.narg(event_id)::uuid)
 AND e.status='ignored' AND e.last_error='paths_filtered' AND e.path_decision->>'outcome'='skip'
ORDER BY e.created_at DESC,e.id LIMIT sqlc.arg(max_rows)::integer;

-- name: GetCISourceCheck :one
SELECT * FROM ci_automation_path_checks WHERE account_id=$1 AND event_id=$2;

-- name: TryLockCISourceCheck :one
SELECT * FROM ci_automation_path_checks WHERE account_id=$1 AND event_id=$2 FOR UPDATE SKIP LOCKED;

-- name: GetCISourceCheckRetryByKey :one
SELECT * FROM ci_source_check_retries WHERE account_id=$1 AND key_digest=$2;

-- name: GetCISourceCheckRetryByGeneration :one
SELECT * FROM ci_source_check_retries WHERE account_id=$1 AND event_id=$2 AND generation=$3;

-- name: InsertCISourceCheckRetry :one
INSERT INTO ci_source_check_retries(id,account_id,event_id,generation,key_digest) VALUES($1,$2,$3,$4,$5) RETURNING *;

-- githubd consumes immutable apid intent without changing the source decision,
-- projection, existing remote identity or any execution/compute state.
-- name: ApplyCISourceCheckRetry :exec
WITH candidate AS (
 SELECT c.event_id,r.id,r.generation FROM ci_automation_path_checks c JOIN ci_source_check_retries r
 ON r.account_id=c.account_id AND r.event_id=c.event_id AND r.generation=c.retry_generation+1
 WHERE c.account_id=$1 AND c.status='dead' AND c.attempts>=sqlc.arg(max_attempts)::integer
 AND c.last_error IN ('publish_failed','unavailable','attempts_exhausted') AND r.applied_at IS NULL
 ORDER BY r.requested_at,r.id FOR UPDATE OF c,r SKIP LOCKED LIMIT 1
), reset AS (
 UPDATE ci_automation_path_checks c SET status='pending',attempts=0,last_error='',lease_token=NULL,lease_until=NULL,
 next_attempt_at=clock_timestamp(),retry_generation=x.generation FROM candidate x
 WHERE c.event_id=x.event_id RETURNING x.id
)
UPDATE ci_source_check_retries r SET applied_at=clock_timestamp() FROM reset x WHERE r.id=x.id;
