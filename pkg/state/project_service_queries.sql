-- name: ProjectServiceAppIDs :many
SELECT id FROM apps
WHERE account_id = sqlc.arg(account_id) AND project_id = sqlc.arg(project_id)
  AND preview_of_slug IS NULL AND status <> 'deleted'
  AND translate(lower(workload_name), '_.', '--') = translate(lower(sqlc.arg(workload_name)::text), '_.', '--')
ORDER BY id
LIMIT 2;

-- name: DeploymentActivationServingMatches :one
SELECT (count(*) = 1 AND coalesce(bool_and(
  d.traffic_percent = 100 AND d.id IN (sqlc.arg(target_id)::uuid, sqlc.arg(expected_id)::uuid)
), false))::boolean AS matches
FROM deployments d
WHERE d.app_id = sqlc.arg(app_id)::uuid
  AND coalesce(nullif(d.scope, ''), 'default') = sqlc.arg(scope)::text
  AND d.status = 'live' AND d.deleted_at IS NULL AND d.traffic_percent > 0;

-- name: ProjectHasNamedStages :one
SELECT EXISTS (SELECT 1 FROM project_environments
WHERE project_id = sqlc.arg(project_id)::uuid AND slug NOT IN ('', 'default', 'production'))::boolean AS stages;

-- name: DeleteOwnedProject :execrows
DELETE FROM projects WHERE account_id = sqlc.arg(account_id)::uuid AND id = sqlc.arg(project_id)::uuid;

-- name: ProjectServiceHasDeletedApp :one
-- A deleted production member keeps its service name until it is purged, so
-- the name does not fall back to an app slug elsewhere (ErrProjectServiceDeleted).
SELECT EXISTS (
  SELECT 1 FROM apps
  WHERE account_id = sqlc.arg(account_id) AND project_id = sqlc.arg(project_id)
    AND preview_of_slug IS NULL AND status = 'deleted'
    AND translate(lower(workload_name), '_.', '--') = translate(lower(sqlc.arg(workload_name)::text), '_.', '--')
)::boolean AS deleted;

-- name: EnvironmentOnlyProjectAppIDs :many
-- A stage-owned head without any production admission is not a production
-- removal candidate. Keep legacy project members under the existing policy.
SELECT a.id::text FROM apps a
WHERE a.account_id = sqlc.arg(account_id) AND a.project_id = sqlc.arg(project_id)
  AND a.preview_of_slug IS NULL AND a.status <> 'deleted'
  AND coalesce(a.manifest->>'project_source_sha256', '') = ''
  AND (EXISTS (SELECT 1 FROM project_app_stage_admissions origin
    WHERE origin.app_id = a.id AND origin.project_id = a.project_id) OR EXISTS (
    SELECT 1 FROM project_environment_workload_heads h
    JOIN project_environments e ON e.id = h.environment_id
    WHERE h.app_id = a.id AND e.project_id = a.project_id AND e.slug <> 'production'
  ))
  AND NOT EXISTS (
    SELECT 1 FROM project_environment_workload_heads h
    JOIN project_environments e ON e.id = h.environment_id
    WHERE h.app_id = a.id AND e.project_id = a.project_id AND e.slug = 'production'
  )
  AND NOT EXISTS (
    SELECT 1 FROM deployments d WHERE d.app_id = a.id AND d.scope IN ('', 'default', 'production')
  )
ORDER BY a.id;

-- name: ProjectEnvironmentAppIDs :many
SELECT a.id::text FROM apps a
WHERE a.account_id = sqlc.arg(account_id) AND a.project_id = sqlc.arg(project_id)
  AND a.preview_of_slug IS NULL AND a.status <> 'deleted'
  AND CASE WHEN sqlc.arg(environment)::text = 'production' THEN NOT (
    coalesce(a.manifest->>'project_source_sha256', '') = ''
    AND (EXISTS (SELECT 1 FROM project_app_stage_admissions origin
      WHERE origin.app_id = a.id AND origin.project_id = a.project_id) OR EXISTS (
      SELECT 1 FROM project_environment_workload_heads h
      JOIN project_environments e ON e.id = h.environment_id
      WHERE h.app_id = a.id AND e.project_id = a.project_id AND e.slug <> 'production'
    ))
    AND NOT EXISTS (
      SELECT 1 FROM project_environment_workload_heads h
      JOIN project_environments e ON e.id = h.environment_id
      WHERE h.app_id = a.id AND e.project_id = a.project_id AND e.slug = 'production'
    )
    AND NOT EXISTS (SELECT 1 FROM deployments d WHERE d.app_id = a.id AND d.scope IN ('', 'default', 'production'))
  ) ELSE (
    EXISTS (
      SELECT 1 FROM project_environment_workload_heads h
      JOIN project_environments e ON e.id = h.environment_id
      WHERE h.app_id = a.id AND e.project_id = a.project_id AND e.slug = sqlc.arg(environment)::text
    ) OR EXISTS (SELECT 1 FROM deployments d WHERE d.app_id = a.id AND d.scope = sqlc.arg(environment)::text AND d.deleted_at IS NULL)
  ) END
ORDER BY a.id;
