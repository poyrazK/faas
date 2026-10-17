-- name: GetCIAutomationPolicy :one
SELECT * FROM ci_automation_policies WHERE account_id=$1 AND binding_id=$2;

-- name: ListCIAutomationPolicies :many
SELECT * FROM ci_automation_policies WHERE account_id=$1 ORDER BY created_at DESC,id LIMIT sqlc.arg(max_rows)::integer;

-- name: UpsertCIAutomationPolicy :one
INSERT INTO ci_automation_policies(id,account_id,binding_id,binding_generation,profile_id,push_branches,latest_only,pr_branches,include_paths,exclude_paths)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
ON CONFLICT(account_id,binding_id) DO UPDATE SET binding_generation=EXCLUDED.binding_generation,
 profile_id=EXCLUDED.profile_id,push_branches=EXCLUDED.push_branches,pr_branches=EXCLUDED.pr_branches,latest_only=EXCLUDED.latest_only,
 include_paths=EXCLUDED.include_paths,exclude_paths=EXCLUDED.exclude_paths,enabled=true,generation=ci_automation_policies.generation+1
RETURNING *;

-- name: DisableCIAutomationPolicy :one
UPDATE ci_automation_policies SET enabled=false,generation=generation+CASE WHEN enabled THEN 1 ELSE 0 END
WHERE account_id=$1 AND binding_id=$2 RETURNING *;

-- name: IgnoreCIAutomationBindingEvents :exec
UPDATE ci_automation_events SET status='ignored',last_error=$3,lease_token=NULL,lease_until=NULL
WHERE account_id=$1 AND binding_id=$2 AND status IN ('pending','processing');

-- name: IgnoreCIAutomationProfileEvents :exec
UPDATE ci_automation_events SET status='ignored',last_error='profile_disabled',lease_token=NULL,lease_until=NULL
WHERE account_id=$1 AND profile_id=$2 AND status IN ('pending','processing');

-- name: GetCIAutomationEventByKey :one
SELECT * FROM ci_automation_events WHERE account_id=$1 AND binding_id=$2 AND event_key=$3;

-- name: InsertCIAutomationEvent :exec
INSERT INTO ci_automation_events(id,account_id,binding_id,policy_id,policy_generation,profile_id,event_key,snapshot)
VALUES($1,$2,$3,$4,$5,$6,$7,$8);

-- name: ExpireCIAutomationEvents :exec
UPDATE ci_automation_events SET status='dead',last_error=CASE WHEN last_error IN ('','branch_wait','pr_wait') THEN 'unavailable' ELSE last_error END,
 lease_token=NULL,lease_until=NULL
WHERE account_id=$1 AND attempts>=sqlc.arg(max_attempts)::integer
 AND (status='pending' OR (status='processing' AND lease_until<=clock_timestamp()));

-- name: ClaimCIAutomationEvent :one
WITH candidate AS (
 SELECT w.id FROM ci_automation_events w WHERE w.account_id=$1 AND w.attempts<sqlc.arg(max_attempts)::integer
 AND ((w.status='pending' AND w.next_attempt_at<=clock_timestamp()) OR (w.status='processing' AND w.lease_until<=clock_timestamp()))
 ORDER BY w.next_attempt_at,w.created_at,w.id LIMIT 1 FOR UPDATE OF w SKIP LOCKED
)
UPDATE ci_automation_events e SET status='processing',attempts=e.attempts+1,lease_token=sqlc.arg(token)::uuid,
 lease_until=clock_timestamp()+make_interval(secs=>sqlc.arg(lease_seconds)::integer)
FROM candidate c WHERE e.id=c.id RETURNING e.*;

-- name: LockCIAutomationEvent :one
SELECT * FROM ci_automation_events WHERE account_id=$1 AND id=$2 FOR UPDATE;

-- name: AdmitCIAutomationEvent :execrows
UPDATE ci_automation_events SET status='admitted',execution_id=$4,lease_token=NULL,lease_until=NULL,last_error='',path_decision=$5
WHERE account_id=$1 AND id=$2 AND lease_token=$3 AND status='processing' AND lease_until>clock_timestamp();

-- name: FailCIAutomationEvent :execrows
UPDATE ci_automation_events SET status=$4,last_error=$5,next_attempt_at=$6,lease_token=NULL,lease_until=NULL
WHERE account_id=$1 AND id=$2 AND lease_token=$3 AND status='processing' AND lease_until>clock_timestamp();

-- name: ListCIAutomationEvents :many
SELECT e.id,e.policy_id,e.policy_generation,e.profile_id,e.status,e.attempts,e.last_error,e.execution_id,e.created_at,
 (e.snapshot->'source'->>'commit_sha')::text AS commit_sha,(e.snapshot->'source'->>'branch')::text AS branch,
 (e.snapshot->'source'->>'kind')::text AS kind,coalesce((e.snapshot->'source'->>'pr_number')::bigint,0)::bigint AS pr_number,
 e.retry_generation,e.next_attempt_at,e.lease_until,e.superseded_by,e.superseded_sha,e.superseded_at,e.path_decision,
 EXISTS(SELECT 1 FROM ci_executions x WHERE x.account_id=e.account_id AND x.idempotency_key='github-auto:' || e.id::text) AS has_execution,
 r.id AS retry_id,coalesce(r.generation,0)::integer AS last_retry_generation,
 coalesce(r.previous_attempts,0)::integer AS previous_attempts,coalesce(r.previous_error,'')::text AS previous_error,r.requested_at
FROM ci_automation_events e LEFT JOIN LATERAL (
 SELECT id,generation,previous_attempts,previous_error,requested_at FROM ci_automation_event_retries
 WHERE account_id=e.account_id AND event_id=e.id ORDER BY generation DESC LIMIT 1
) r ON true
WHERE e.account_id=$1 AND e.binding_id=$2
ORDER BY e.created_at DESC,e.id LIMIT sqlc.arg(max_rows)::integer;
