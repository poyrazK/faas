-- name: BeginCIAutomationHead :one
INSERT INTO ci_automation_branches(account_id,binding_id,branch,head_fence) VALUES($1,$2,$3,1)
ON CONFLICT(account_id,binding_id,branch) DO UPDATE SET head_fence=ci_automation_branches.head_fence+1 RETURNING head_fence;

-- name: InvalidateCIAutomationHead :exec
UPDATE ci_automation_branches SET head_fence=head_fence+1 WHERE account_id=$1 AND binding_id=$2 AND branch=$3;

-- name: GetCIAutomationHeadFence :one
SELECT head_fence FROM ci_automation_branches WHERE account_id=$1 AND binding_id=$2 AND branch=$3;

-- name: DeferCIAutomationScope :execrows
UPDATE ci_automation_events SET status='pending',attempts=attempts-1,last_error=sqlc.arg(reason)::text,lease_token=NULL,lease_until=NULL,
 next_attempt_at=clock_timestamp()+make_interval(secs=>sqlc.arg(poll_seconds)::integer)
WHERE account_id=$1 AND id=$2 AND status='processing' AND lease_token=$3 AND lease_until>clock_timestamp();

-- name: FindCIAutomationHeadEvent :one
SELECT id FROM ci_automation_events WHERE account_id=$1 AND binding_id=$2 AND policy_id=$3 AND policy_generation=$4
 AND snapshot->'source'->>'kind'='push'
 AND snapshot->'source'->>'branch'=sqlc.arg(branch)::text AND snapshot->'source'->>'commit_sha'=sqlc.arg(commit_sha)::text
ORDER BY created_at,id LIMIT 1;

-- name: IgnoreCIAutomationOldHead :execrows
UPDATE ci_automation_events SET status='ignored',last_error=$4,lease_token=NULL,lease_until=NULL,
 superseded_sha=nullif(sqlc.arg(head_sha)::text,''),superseded_at=CASE WHEN sqlc.arg(head_sha)::text='' THEN NULL ELSE clock_timestamp() END,
 superseded_by=sqlc.narg(superseded_by)::uuid
WHERE account_id=$1 AND id=$2 AND status='processing' AND lease_token=$3 AND lease_until>clock_timestamp();

-- name: SupersedeCIAutomationQueued :exec
UPDATE ci_automation_events SET status='ignored',last_error='head_changed',lease_token=NULL,lease_until=NULL,
 superseded_by=sqlc.arg(head_event)::uuid,superseded_sha=sqlc.arg(head_sha)::text,superseded_at=clock_timestamp()
WHERE account_id=$1 AND binding_id=$2 AND policy_id=$3 AND policy_generation=$4
 AND snapshot->'source'->>'kind'='push'
 AND snapshot->'source'->>'branch'=sqlc.arg(branch)::text AND id<>sqlc.arg(head_event)::uuid
 AND status IN ('pending','processing','dead') AND execution_id IS NULL;

-- Account lock serializes admission. Terminal executions can still hold a slot
-- until their native and transfer receipts confirm cleanup.
-- name: LockCIAutomationBranchExecutions :many
SELECT x.* FROM ci_automation_events e JOIN ci_executions x ON x.account_id=e.account_id AND x.id=e.execution_id
WHERE e.account_id=$1 AND e.binding_id=$2 AND e.snapshot->'source'->>'kind'='push'
 AND e.snapshot->'source'->>'branch'=sqlc.arg(branch)::text AND x.archived_at IS NULL
ORDER BY x.id FOR UPDATE OF x;

-- name: MarkCIAutomationSupersededExecution :exec
UPDATE ci_automation_events SET superseded_by=$3,superseded_sha=$4,superseded_at=clock_timestamp()
WHERE account_id=$1 AND execution_id=$2 AND superseded_at IS NULL;

-- Retained artifacts do not hold a branch slot. A terminal CI status alone
-- cannot certify native stop, sealed output or completion of in-flight I/O.
-- name: CIAutomationExecutionStopped :one
SELECT NOT EXISTS(SELECT 1 FROM ci_job_attempts a LEFT JOIN job_tasks t ON t.run_id=a.attempt_id AND t.task_index=0
 LEFT JOIN instances i ON i.id=t.instance_id WHERE a.account_id=$1 AND a.execution_id=$2
 AND ((a.run_id IS NULL AND t.run_id IS NOT NULL) OR (a.run_id IS NOT NULL
 AND (t.run_id IS NULL OR t.attempt<>1 OR t.status NOT IN ('succeeded','failed','cancelled')
 OR (t.instance_id IS NOT NULL AND (i.id IS NULL OR i.state<>'stopped' OR a.output IS NULL))))))
 AND NOT EXISTS(SELECT 1 FROM ci_job_runtime r JOIN ci_job_attempts a ON a.attempt_id=r.attempt_id
 LEFT JOIN instances i ON i.id=r.instance_id WHERE a.account_id=$1 AND a.execution_id=$2
 AND (i.id IS NULL OR i.state<>'stopped' OR r.transfer_until>clock_timestamp())) AS safe;
