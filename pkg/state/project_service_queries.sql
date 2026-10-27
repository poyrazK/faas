-- name: ProjectServiceAppIDs :many
SELECT id FROM apps
WHERE account_id = sqlc.arg(account_id) AND project_id = sqlc.arg(project_id)
  AND preview_of_slug IS NULL AND status <> 'deleted'
  AND translate(lower(workload_name), '_.', '--') = translate(lower(sqlc.arg(workload_name)::text), '_.', '--')
ORDER BY id
LIMIT 2;
