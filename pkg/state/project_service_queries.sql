-- name: ProjectServiceAppIDs :many
SELECT id FROM apps
WHERE account_id = sqlc.arg(account_id) AND project_id = sqlc.arg(project_id)
  AND preview_of_slug IS NULL AND status <> 'deleted'
  AND translate(lower(workload_name), '_.', '--') = translate(lower(sqlc.arg(workload_name)::text), '_.', '--')
ORDER BY id
LIMIT 2;

-- name: ProjectServiceHasDeletedApp :one
-- A deleted production member keeps its service name until it is purged, so
-- the name does not fall back to an app slug elsewhere (ErrProjectServiceDeleted).
SELECT EXISTS (
  SELECT 1 FROM apps
  WHERE account_id = sqlc.arg(account_id) AND project_id = sqlc.arg(project_id)
    AND preview_of_slug IS NULL AND status = 'deleted'
    AND translate(lower(workload_name), '_.', '--') = translate(lower(sqlc.arg(workload_name)::text), '_.', '--')
)::boolean AS deleted;
