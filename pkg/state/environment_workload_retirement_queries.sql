-- name: ListEnvironmentWorkloadRetirements :many
SELECT DISTINCT ON (resource) * FROM environment_workload_retirements
WHERE source_id=sqlc.arg(source_id)::uuid AND account_id=sqlc.arg(account_id)::uuid
ORDER BY resource,generation DESC;

-- name: LockEnvironmentWorkloadRetirement :one
SELECT * FROM environment_workload_retirements
WHERE source_id=sqlc.arg(source_id)::uuid AND id=sqlc.arg(id)::uuid FOR UPDATE;

-- name: InsertEnvironmentWorkloadRetirement :one
INSERT INTO environment_workload_retirements
 (source_id,account_id,project_id,environment_id,resource,app_id,revision_id,generation,plan_hash,
  deployment_ids,queue_binding_ids,job_id,route_deployment_id)
VALUES (sqlc.arg(source_id)::uuid,sqlc.arg(account_id)::uuid,sqlc.arg(project_id)::uuid,
 sqlc.arg(environment_id)::uuid,sqlc.arg(resource)::text,sqlc.arg(app_id)::uuid,
 sqlc.arg(revision_id)::uuid,sqlc.arg(generation)::bigint,sqlc.arg(plan_hash)::text,
 sqlc.arg(deployment_ids)::uuid[],sqlc.arg(queue_binding_ids)::uuid[],sqlc.narg(job_id)::uuid,sqlc.narg(route_deployment_id)::uuid)
RETURNING *;

-- name: EnvironmentWorkloadAdmissionRetired :one
SELECT environment_workload_admission_retired(sqlc.arg(app_id)::uuid,sqlc.arg(scope)::text)::boolean;

-- name: PrepareEnvironmentWorkloadRetirementGateways :one
UPDATE environment_workload_retirements SET route_generation=sqlc.arg(route_generation)::bigint,
 expected_gateways=sqlc.arg(expected_gateways)::text[],acknowledged_gateways='{}'
WHERE id=sqlc.arg(id)::uuid AND source_id=sqlc.arg(source_id)::uuid AND phase IN ('retiring','retired','recovering')
RETURNING *;

-- name: AcknowledgeEnvironmentWorkloadRetirementGateway :execrows
UPDATE environment_workload_retirements SET acknowledged_gateways=ARRAY(
 SELECT DISTINCT node FROM unnest(acknowledged_gateways||ARRAY[sqlc.arg(node)::text]) node ORDER BY node)
WHERE id=sqlc.arg(id)::uuid AND source_id=sqlc.arg(source_id)::uuid
 AND route_generation=sqlc.arg(route_generation)::bigint AND route_generation>0
 AND sqlc.arg(node)::text=ANY(expected_gateways) AND phase IN ('retiring','retired','recovering');

-- name: StartEnvironmentWorkloadRetirement :one
UPDATE environment_workload_retirements SET phase='retiring'
WHERE id=sqlc.arg(id)::uuid AND source_id=sqlc.arg(source_id)::uuid AND phase='planned'
RETURNING *;

-- name: CancelPlannedEnvironmentWorkloadRetirement :execrows
UPDATE environment_workload_retirements SET phase='cancelled'
WHERE id=sqlc.arg(id)::uuid AND source_id=sqlc.arg(source_id)::uuid AND phase='planned';

-- name: CompleteEnvironmentWorkloadRetirement :one
UPDATE environment_workload_retirements SET phase='retired',retired_at=clock_timestamp()
WHERE id=sqlc.arg(id)::uuid AND source_id=sqlc.arg(source_id)::uuid AND phase='retiring'
RETURNING *;

-- name: BeginEnvironmentWorkloadRecovery :one
UPDATE environment_workload_retirements SET phase='recovering',recovery_revision_id=sqlc.arg(revision_id)::uuid,
 recovery_generation=sqlc.arg(generation)::bigint
WHERE id=sqlc.arg(id)::uuid AND source_id=sqlc.arg(source_id)::uuid AND phase IN ('retired','recovering')
RETURNING *;

-- name: ActivateEnvironmentWorkloadRecovery :execrows
UPDATE environment_workload_retirements SET phase='recovered',recovered_at=clock_timestamp()
WHERE source_id=sqlc.arg(source_id)::uuid AND app_id=sqlc.arg(app_id)::uuid AND environment_id=sqlc.arg(environment_id)::uuid
 AND phase='recovering' AND recovery_revision_id=sqlc.arg(revision_id)::uuid AND recovery_generation=sqlc.arg(generation)::bigint;

-- Original execution identities are frozen while the scoped app lock is held.
-- name: EnvironmentWorkloadRetirementInventory :one
SELECT jsonb_build_object(
 'deployment_ids',coalesce((SELECT jsonb_agg(d.id::text ORDER BY d.id) FROM deployments d
  WHERE d.app_id=r.app_id AND d.scope=e.slug AND d.environment_workload_runtime->>'source_id'=s.id::text),'[]'::jsonb),
 'queue_binding_ids',coalesce((SELECT jsonb_agg(q.binding_id::text ORDER BY q.binding_id) FROM environment_gitops_queue_bindings q
  WHERE q.source_id=s.id AND q.resource=r.logical_name AND q.binding_id IS NOT NULL),'[]'::jsonb),
 'route_deployment_id',coalesce((SELECT d.id::text FROM deployments d WHERE d.app_id=r.app_id AND d.scope=e.slug
  AND d.environment_workload_runtime->>'source_id'=s.id::text AND NOT d.environment_workload_held
  AND coalesce(d.environment_workload_runtime->'runtime'->>'execution_mode',d.environment_workload_runtime->'baseline'->>'execution_mode','request')<>'job'
  ORDER BY d.id LIMIT 1),''),
 'job_id',coalesce((SELECT w.job_id::text FROM app_environment_workload_intents w WHERE w.app_id=r.app_id AND w.environment_id=e.id),''),
 'unmanaged_execution',EXISTS(SELECT 1 FROM deployments d WHERE d.app_id=r.app_id AND d.scope=e.slug
  AND d.status='live' AND d.environment_workload_runtime->>'source_id' IS DISTINCT FROM s.id::text),
 'unmanaged_binding',EXISTS(SELECT 1 FROM queue_bindings b WHERE b.app_id=r.app_id AND b.environment_id=e.id
  AND NOT EXISTS(SELECT 1 FROM environment_gitops_queue_bindings q WHERE q.binding_id=b.id AND q.source_id=s.id AND q.resource=r.logical_name)))::jsonb
FROM environment_gitops_resources r JOIN environment_git_sources s ON s.id=r.source_id
JOIN project_environments e ON e.id=s.environment_id
WHERE s.id=sqlc.arg(source_id)::uuid AND r.logical_name=sqlc.arg(resource)::text;

-- name: EnvironmentWorkloadRetirementGatewayRoster :one
SELECT environment_workload_retirement_gateway_roster()::text[];

-- name: EnvironmentWorkloadRetirementWithdrawn :one
SELECT environment_workload_retirement_routes_withdrawn(r)::boolean FROM environment_workload_retirements r
WHERE r.id=sqlc.arg(id)::uuid AND r.source_id=sqlc.arg(source_id)::uuid;

-- name: DrainEnvironmentWorkloadRetirementDeployments :execrows
UPDATE deployments d SET status=CASE WHEN d.status='live' THEN 'superseded' ELSE 'cancelled' END,
 traffic_percent=0,traffic_percent_explicit=true
FROM environment_workload_retirements r WHERE r.id=sqlc.arg(id)::uuid AND r.source_id=sqlc.arg(source_id)::uuid
 AND r.phase='retiring' AND d.id=ANY(r.deployment_ids) AND d.status NOT IN ('failed','superseded','cancelled')
 AND environment_workload_retirement_routes_withdrawn(r);

-- name: EnvironmentWorkloadRetirementDrained :one
SELECT environment_workload_retirement_drained(r)::boolean FROM environment_workload_retirements r
WHERE r.id=sqlc.arg(id)::uuid AND r.source_id=sqlc.arg(source_id)::uuid;

-- name: PauseEnvironmentWorkloadRetirementJob :exec
UPDATE jobs SET status='paused',updated_at=clock_timestamp()
WHERE id=(SELECT job_id FROM environment_workload_retirements WHERE id=sqlc.arg(id)::uuid AND source_id=sqlc.arg(source_id)::uuid)
 AND status='active';

-- name: AbandonEnvironmentWorkloadRecovery :execrows
UPDATE environment_workload_retirements SET phase='retired',recovery_revision_id=NULL,recovery_generation=0
WHERE id=sqlc.arg(id)::uuid AND source_id=sqlc.arg(source_id)::uuid AND phase='recovering';

-- name: EnvironmentWorkloadRetirementReleaseExclusions :many
SELECT r.app_id FROM environment_workload_retirements r JOIN environment_git_sources s ON s.id=r.source_id
 JOIN project_environments e ON e.id=r.environment_id JOIN environment_desired_revisions rev ON rev.id=s.approved_revision_id
WHERE r.account_id=sqlc.arg(account_id)::uuid AND r.project_id=sqlc.arg(project_id)::uuid AND e.slug=sqlc.arg(scope)::text
 AND (r.phase IN ('retiring','retired') OR r.phase='planned' AND NOT s.detached AND s.mode='enforce'
  AND r.generation=s.generation AND r.revision_id=rev.id AND NOT (rev.definition->'workloads' ? substring(r.resource from 10)))
ORDER BY r.app_id;

-- name: SetEnvironmentWorkloadRetirementCutoverContext :one
SELECT set_config('gregale.gitops_activation',sqlc.arg(token)::text,true)::text;

-- name: LockEnvironmentWorkloadRetirementProject :one
SELECT id FROM projects WHERE id=sqlc.arg(project_id)::uuid AND account_id=sqlc.arg(account_id)::uuid FOR UPDATE;

-- name: WithdrawEnvironmentWorkloadRetirementTraffic :exec
UPDATE deployments d SET traffic_percent=0,traffic_percent_explicit=true
FROM environment_workload_retirements r WHERE r.id=sqlc.arg(id)::uuid AND r.source_id=sqlc.arg(source_id)::uuid
 AND r.phase='retiring' AND d.id=ANY(r.deployment_ids) AND d.status='live';
