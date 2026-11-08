-- name: ReadEnvironmentRollbackReceipt :one
SELECT attempt_id, request_hash, promotion_hash, project_id::text AS project_id,
       from_environment_id::text AS from_environment_id, environment_id::text AS environment_id,
       promotion_id::text AS promotion_id, accepted_at
FROM project_environment_rollback_receipts
WHERE account_id = sqlc.arg(account_id)::text::uuid AND promotion_id = sqlc.arg(promotion_id)::text::uuid;

-- name: InsertEnvironmentRollbackReceipt :exec
INSERT INTO project_environment_rollback_receipts
(account_id, promotion_id, attempt_id, request_hash, promotion_hash, project_id, from_environment_id, environment_id, accepted_at)
VALUES (sqlc.arg(account_id)::text::uuid, sqlc.arg(promotion_id)::text::uuid, sqlc.arg(attempt_id), sqlc.arg(request_hash),
 sqlc.arg(promotion_hash), sqlc.arg(project_id)::text::uuid, sqlc.arg(from_environment_id)::text::uuid,
 sqlc.arg(environment_id)::text::uuid, sqlc.arg(accepted_at));

-- name: EnvironmentRollbackScope :one
SELECT source.id::text AS from_environment_id, target.id::text AS environment_id
FROM projects p
JOIN project_environments source ON source.project_id = p.id AND source.account_id = p.account_id
JOIN project_environments target ON target.project_id = p.id AND target.account_id = p.account_id
WHERE p.id = sqlc.arg(project_id)::text::uuid AND p.account_id = sqlc.arg(account_id)::text::uuid
 AND p.slug = sqlc.arg(project_slug) AND source.slug = sqlc.arg(from_environment) AND target.slug = sqlc.arg(environment)
FOR SHARE OF p, source, target;
