-- name: SkipCIAutomationPaths :execrows
UPDATE ci_automation_events SET status='ignored',last_error='paths_filtered',path_decision=$4,lease_token=NULL,lease_until=NULL
WHERE account_id=$1 AND id=$2 AND lease_token=$3 AND status='processing' AND lease_until>clock_timestamp();

-- A latest-only skip cannot strand code changes that have not acquired an
-- execution. The replacement head must run before those sources supersede.
-- name: CIAutomationUnsettledPushSources :one
SELECT EXISTS(SELECT 1 FROM ci_automation_events e WHERE e.account_id=$1 AND e.binding_id=$2 AND e.policy_id=$3 AND e.policy_generation=$4
 AND e.id<>$5 AND e.snapshot->'source'->>'kind'='push' AND e.snapshot->'source'->>'branch'=sqlc.arg(branch)::text
 AND (e.status IN ('pending','processing','dead') OR (e.status='ignored' AND e.last_error='head_changed'
 AND NOT EXISTS(SELECT 1 FROM ci_automation_events covered JOIN ci_executions x ON x.account_id=covered.account_id AND x.id=covered.execution_id
  WHERE covered.account_id=e.account_id AND covered.binding_id=e.binding_id AND covered.policy_id=e.policy_id AND covered.policy_generation=e.policy_generation
  AND covered.status='admitted' AND covered.snapshot->'source'->>'kind'='push'
  AND covered.snapshot->'source'->>'branch'=e.snapshot->'source'->>'branch'
  AND (covered.snapshot->'source'->>'commit_sha'=e.superseded_sha OR x.created_at>e.superseded_at)))));

-- name: InsertCIAutomationPathCheck :exec
INSERT INTO ci_automation_path_checks(account_id,event_id,snapshot) VALUES($1,$2,$3);

-- name: ExpireCIAutomationPathChecks :exec
WITH expired AS (
 SELECT w.event_id FROM ci_automation_path_checks w WHERE w.account_id=$1 AND w.status='sending'
 AND w.attempts>=sqlc.arg(max_attempts)::integer AND w.lease_until<=clock_timestamp() FOR UPDATE OF w SKIP LOCKED
)
UPDATE ci_automation_path_checks c SET status='dead',last_error='attempts_exhausted',lease_token=NULL,lease_until=NULL
FROM expired x WHERE c.event_id=x.event_id;

-- name: ClaimCIAutomationPathCheck :one
WITH candidate AS (
 SELECT w.event_id FROM ci_automation_path_checks w WHERE w.account_id=$1 AND w.attempts<sqlc.arg(max_attempts)::integer
 AND ((w.status='pending' AND w.next_attempt_at<=clock_timestamp()) OR (w.status='sending' AND w.lease_until<=clock_timestamp()))
 ORDER BY w.next_attempt_at,w.created_at,w.event_id FOR UPDATE OF w SKIP LOCKED LIMIT 1
)
UPDATE ci_automation_path_checks c SET status='sending',attempts=attempts+1,lease_token=sqlc.arg(token)::uuid,
 lease_until=clock_timestamp()+make_interval(secs=>sqlc.arg(lease_seconds)::integer)
FROM candidate x WHERE c.event_id=x.event_id RETURNING c.*;

-- name: LockCIAutomationPathCheck :one
SELECT * FROM ci_automation_path_checks WHERE account_id=$1 AND event_id=$2 FOR UPDATE;

-- name: FinishCIAutomationPathCheck :execrows
UPDATE ci_automation_path_checks SET status='sent',check_id=$4,lease_token=NULL,lease_until=NULL,last_error=''
WHERE account_id=$1 AND event_id=$2 AND lease_token=$3 AND status='sending' AND lease_until>clock_timestamp();

-- name: FailCIAutomationPathCheck :execrows
UPDATE ci_automation_path_checks SET status=$4,last_error=$5,next_attempt_at=$6,lease_token=NULL,lease_until=NULL
WHERE account_id=$1 AND event_id=$2 AND lease_token=$3 AND status='sending' AND lease_until>clock_timestamp();
