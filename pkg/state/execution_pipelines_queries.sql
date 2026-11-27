-- name: PipelineJobExists :one
SELECT EXISTS(SELECT 1 FROM agent_execution_workflows WHERE account_id=$1 AND workflow_id=$2 AND runs_principal_id IS NOT DISTINCT FROM sqlc.narg(principal_id)::uuid);
-- name: PipelineActiveCount :one
SELECT count(*) FROM agent_execution_workflows WHERE account_id=$1 AND status IN ('queued','running');
-- name: PipelineJobInsert :one
INSERT INTO agent_execution_workflows(account_id,runs_principal_id,workflow_id,plan_id,step_count,sealed_plan,payload_kid,created_at,updated_at,scheduled_for)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$8,'9999-12-31T00:00:00Z') RETURNING id;
-- name: PipelineInsert :exec
INSERT INTO execution_workflow_pipelines(job_id,policy,deadline_at,due_at,journal) VALUES($1,$2,$3,$4,$5);
-- name: PipelineGet :one
SELECT * FROM execution_workflow_pipelines WHERE job_id=$1;
-- name: PipelineLock :one
SELECT * FROM execution_workflow_pipelines WHERE job_id=$1 FOR UPDATE;
-- name: PipelineJobLock :one
SELECT * FROM agent_execution_workflows WHERE id=$1 FOR UPDATE;
-- name: PipelineClaim :one
WITH protocol AS MATERIALIZED (
 SELECT set_config('faas.run_pipeline_progress_protocol','1',true),
        set_config('faas.run_pipeline_scoped_file_protocol','1',true)
), target AS (
 SELECT j.id FROM agent_execution_workflows j JOIN execution_workflow_pipelines p ON p.job_id=j.id CROSS JOIN protocol
 WHERE j.status IN ('queued','running') AND p.due_at<=sqlc.arg(now_at)::timestamptz
 AND (j.lease_expires_at IS NULL OR j.lease_expires_at<=sqlc.arg(now_at)::timestamptz)
 ORDER BY p.due_at,j.created_at,j.id FOR UPDATE OF j SKIP LOCKED LIMIT 1
)
UPDATE agent_execution_workflows j SET status='running',lease_token=sqlc.arg(token)::uuid,lease_owner=sqlc.arg(owner)::text,
 lease_expires_at=sqlc.arg(expires_at)::timestamptz,updated_at=sqlc.arg(now_at)::timestamptz
FROM target WHERE j.id=target.id RETURNING j.*;
-- name: PipelineUpdateJob :execrows
UPDATE agent_execution_workflows SET status=$3,next_step=$4,last_error=$5,updated_at=$6,finished_at=sqlc.narg(finished_at)::timestamptz,
 lease_token=NULL,lease_owner=NULL,lease_expires_at=NULL,
 sealed_plan=CASE WHEN $3 IN ('succeeded','failed') THEN NULL ELSE sealed_plan END,
 payload_kid=CASE WHEN $3 IN ('succeeded','failed') THEN '' ELSE payload_kid END
WHERE id=$1 AND lease_token=$2 AND status='running';
-- name: PipelineUpdateDue :exec
UPDATE execution_workflow_pipelines SET due_at=$2 WHERE job_id=$1;
-- name: PipelineCancel :execrows
UPDATE execution_workflow_pipelines p SET cancel_requested=CASE WHEN j.status IN ('queued','running') THEN true ELSE p.cancel_requested END,
 due_at=CASE WHEN j.status IN ('queued','running') THEN LEAST(p.due_at,sqlc.arg(now_at)::timestamptz) ELSE p.due_at END
FROM agent_execution_workflows j WHERE j.id=p.job_id AND j.account_id=sqlc.arg(account_id)::uuid AND j.workflow_id=sqlc.arg(workflow_id)::text
AND (sqlc.narg(principal_id)::uuid IS NULL OR j.runs_principal_id=sqlc.narg(principal_id)::uuid);
-- name: PipelinePreviousRun :one
SELECT id,account_id,workflow_id,step_label,runs_principal_id,status,failure_code,finished_at FROM executions WHERE id=$1;

-- name: PipelineWriterProtocol :exec
SELECT set_config('faas.run_pipeline_protocol','1',true);

-- name: PipelineSetCompletion :exec
UPDATE execution_workflow_pipelines SET completion=sqlc.arg(completion)::jsonb WHERE job_id=$1 AND completion IS NULL;
-- name: PipelineCallbacksPending :many
SELECT p.*,j.account_id FROM execution_workflow_pipelines p JOIN agent_execution_workflows j ON j.id=p.job_id
WHERE p.policy ? 'callback' AND p.completion IS NOT NULL AND p.delivered_at IS NULL
AND (sqlc.arg(after_id)::text='' OR p.job_id>NULLIF(sqlc.arg(after_id)::text,'')::uuid)
ORDER BY p.job_id LIMIT sqlc.arg(batch_limit)::int;
-- name: PipelineCallbackDelivered :execrows
UPDATE execution_workflow_pipelines p SET delivered_at=COALESCE(p.delivered_at,clock_timestamp())
FROM agent_execution_workflows j WHERE j.id=p.job_id AND j.account_id=$1 AND p.job_id=$2 AND p.policy ? 'callback' AND p.completion IS NOT NULL;
-- name: PipelineDeliveryGet :one
SELECT p.*,j.account_id FROM execution_workflow_pipelines p JOIN agent_execution_workflows j ON j.id=p.job_id WHERE j.account_id=$1 AND p.job_id=$2 AND p.policy ? 'callback';
-- name: PipelineDeliveryForRun :one
SELECT p.*,j.account_id FROM executions e JOIN agent_execution_workflows j
ON j.account_id=e.account_id AND j.workflow_id=e.workflow_id AND j.runs_principal_id IS NOT DISTINCT FROM e.runs_principal_id
JOIN execution_workflow_pipelines p ON p.job_id=j.id
WHERE e.account_id=$1 AND e.id=$2 AND p.policy ? 'callback'
AND EXISTS (SELECT 1 FROM jsonb_array_elements(p.policy->'steps') WITH ORDINALITY AS s(value,idx), generate_series(1,(s.value->'retry'->>'max_attempts')::int) AS a(n)
 WHERE e.step_label='gwf:p2:'||j.plan_id||':p:'||(s.idx-1)::text||':a:'||a.n::text);

-- name: PipelineRunByLabel :one
SELECT * FROM executions WHERE account_id=$1 AND runs_principal_id=$2
AND workflow_id=$3 AND step_label=$4 AND step_label LIKE 'gwf:%'
ORDER BY created_at DESC LIMIT 1;

-- name: PipelineScopedJob :one
SELECT j.* FROM agent_execution_workflows j JOIN execution_workflow_pipelines p ON p.job_id=j.id
WHERE j.account_id=$1 AND j.id=$2 AND (sqlc.narg(principal_id)::uuid IS NULL OR j.runs_principal_id=sqlc.narg(principal_id)::uuid);
-- name: PipelineSetJournal :exec
UPDATE execution_workflow_pipelines SET journal=$2 WHERE job_id=$1;

-- name: PipelineHistoryPage :many
SELECT j.id, j.workflow_id, j.status, j.step_count, j.created_at, j.finished_at,
       COALESCE(p.policy->'rerun'->>'parent_id', '')::text AS parent_id,
       COALESCE(p.completion->>'outcome', j.status)::text AS outcome
FROM agent_execution_workflows j JOIN execution_workflow_pipelines p ON p.job_id=j.id
WHERE j.account_id=sqlc.arg(account_id)
AND (sqlc.narg(before_at)::timestamptz IS NULL OR
     (j.created_at, j.id) < (sqlc.narg(before_at)::timestamptz, sqlc.narg(before_id)::uuid))
ORDER BY j.created_at DESC, j.id DESC
LIMIT sqlc.arg(page_limit)::int;

-- name: PipelineRunUsage :many
SELECT execution_id, created_at, started_at, finished_at, wall_time_ms
FROM execution_usage_ledger
WHERE account_id=sqlc.arg(account_id)
AND execution_id=ANY(sqlc.arg(run_ids)::uuid[]);
