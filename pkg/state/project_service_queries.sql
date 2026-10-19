-- name: ProjectServiceAppIDs :many
SELECT id FROM apps
WHERE account_id = sqlc.arg(account_id) AND project_id = sqlc.arg(project_id)
  AND preview_of_slug IS NULL AND status <> 'deleted'
  AND lower(workload_name) = lower(sqlc.arg(workload_name)::text)
ORDER BY id
LIMIT 2;
