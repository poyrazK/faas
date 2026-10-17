-- name: BeginCIAutomationPullRequest :one
INSERT INTO ci_automation_pull_requests(account_id,binding_id,pr_number,head_fence) VALUES($1,$2,$3,1)
ON CONFLICT(account_id,binding_id,pr_number) DO UPDATE SET head_fence=ci_automation_pull_requests.head_fence+1 RETURNING head_fence;

-- name: InvalidateCIAutomationPullRequest :exec
UPDATE ci_automation_pull_requests SET head_fence=head_fence+1 WHERE account_id=$1 AND binding_id=$2 AND pr_number=$3;

-- name: GetCIAutomationPullRequestFence :one
SELECT head_fence FROM ci_automation_pull_requests WHERE account_id=$1 AND binding_id=$2 AND pr_number=$3;

-- name: FindCIAutomationPullRequestHead :one
SELECT id FROM ci_automation_events WHERE account_id=$1 AND binding_id=$2 AND policy_id=$3 AND policy_generation=$4
 AND snapshot->'source'->>'kind'='pull_request' AND (snapshot->'source'->>'pr_number')::bigint=sqlc.arg(pr_number)::bigint
 AND snapshot->'source'->>'branch'=sqlc.arg(branch)::text AND snapshot->'source'->>'commit_sha'=sqlc.arg(commit_sha)::text
ORDER BY created_at,id LIMIT 1;

-- name: SupersedeCIAutomationPullRequestQueued :exec
UPDATE ci_automation_events SET status='ignored',last_error='head_changed',lease_token=NULL,lease_until=NULL,
 superseded_by=sqlc.arg(head_event)::uuid,superseded_sha=sqlc.arg(head_sha)::text,superseded_at=clock_timestamp()
WHERE account_id=$1 AND binding_id=$2 AND policy_id=$3 AND policy_generation=$4
 AND snapshot->'source'->>'kind'='pull_request' AND (snapshot->'source'->>'pr_number')::bigint=sqlc.arg(pr_number)::bigint
 AND id<>sqlc.arg(head_event)::uuid AND status IN ('pending','processing','dead') AND execution_id IS NULL;

-- PR identity is independent of its target branch and of push automation.
-- name: LockCIAutomationPullRequestExecutions :many
SELECT x.* FROM ci_automation_events e JOIN ci_executions x ON x.account_id=e.account_id AND x.id=e.execution_id
WHERE e.account_id=$1 AND e.binding_id=$2 AND e.snapshot->'source'->>'kind'='pull_request'
 AND (e.snapshot->'source'->>'pr_number')::bigint=sqlc.arg(pr_number)::bigint AND x.archived_at IS NULL
ORDER BY x.id FOR UPDATE OF x;
