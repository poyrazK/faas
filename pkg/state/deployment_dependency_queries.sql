-- name: ReadProjectDependencyOwner :one
SELECT id::text AS app_id, account_id::text AS account_id,
       coalesce(project_id::text, '')::text AS project_id,
       coalesce(preview_of_slug, '')::text AS preview_of_slug,
       coalesce(preview_pr_number, 0)::integer AS preview_pr_number,
       coalesce(manifest, '{}'::jsonb) AS manifest
FROM apps WHERE id = sqlc.arg(app_id)::uuid;

-- name: CaptureProjectDependencyTargets :many
SELECT a.id::text AS app_id, lower(a.workload_name)::text AS service,
       coalesce(d.id::text, '')::text AS deployment_id
FROM apps a
LEFT JOIN LATERAL (
    SELECT id FROM deployments
    WHERE app_id = a.id AND scope = sqlc.arg(scope)::text
      AND environment_workload_runtime IS NULL
    ORDER BY revision DESC, created_at DESC, id DESC LIMIT 1
) d ON true
WHERE a.account_id = sqlc.arg(account_id)::uuid AND a.project_id = sqlc.arg(project_id)::uuid
  AND a.status <> 'deleted' AND lower(a.workload_name) = ANY(sqlc.arg(services)::text[])
  AND coalesce(a.preview_pr_number, 0) = sqlc.arg(preview_pr_number)::integer
  AND (coalesce(a.preview_of_slug, '') <> '') = sqlc.arg(is_preview)::boolean
ORDER BY lower(a.workload_name), a.id;

-- name: InsertDeploymentDependencyGate :exec
INSERT INTO deployment_dependency_gates (deployment_id, pins)
VALUES (sqlc.arg(deployment_id)::uuid, sqlc.arg(pins)::jsonb);

-- name: RetryDeploymentDependencyGate :exec
INSERT INTO deployment_dependency_gates (deployment_id, pins)
SELECT sqlc.arg(deployment_id)::uuid, pins FROM deployment_dependency_gates
WHERE deployment_id = sqlc.arg(source_deployment_id)::uuid;

-- name: ReadDeploymentDependencyGate :one
SELECT g.pins, g.started_at, g.deadline_at, g.status, g.blocker,
       (d.status = 'live' OR d.serving_ended_at IS NOT NULL)::boolean AS previously_served
FROM deployment_dependency_gates g JOIN deployments d ON d.id = g.deployment_id
WHERE g.deployment_id = sqlc.arg(deployment_id)::uuid
FOR UPDATE OF g;

-- name: ReadDependencyGateTarget :one
SELECT d.status, d.traffic_percent, coalesce(d.parked_reason, '')::text AS parked_reason
FROM deployments d JOIN apps a ON a.id = d.app_id
JOIN deployments candidate ON candidate.id = sqlc.arg(candidate_id)::uuid
JOIN apps owner ON owner.id = candidate.app_id
WHERE d.id = sqlc.arg(dependency_id)::uuid AND d.app_id = sqlc.arg(dependency_app_id)::uuid
  AND a.account_id = owner.account_id AND a.project_id = owner.project_id AND a.status <> 'deleted'
  AND a.workload_class <> 'job' AND coalesce(a.manifest->>'execution_mode', '') <> 'job'
  AND coalesce(a.preview_pr_number, 0) = coalesce(owner.preview_pr_number, 0)
  AND (coalesce(a.preview_of_slug, '') = '') = (coalesce(owner.preview_of_slug, '') = '')
  AND d.scope = candidate.scope AND d.environment_workload_runtime IS NULL
FOR SHARE OF d;

-- name: UpdateDeploymentDependencyGate :exec
UPDATE deployment_dependency_gates
SET started_at = sqlc.arg(started_at), deadline_at = sqlc.arg(deadline_at),
    status = sqlc.arg(status)::text, blocker = sqlc.arg(blocker)::text
WHERE deployment_id = sqlc.arg(deployment_id)::uuid;

-- name: ProjectDeploymentDependencyGateProgress :exec
UPDATE deployments
SET stage_state = jsonb_set(stage_state, '{dependency_gate}', sqlc.arg(progress)::jsonb, true)
WHERE id = sqlc.arg(deployment_id)::uuid;
