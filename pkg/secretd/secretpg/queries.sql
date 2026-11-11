-- name: UpsertSecretGrant :one
INSERT INTO secret_grants (app_id, node_id, granted_by_node_id, expires_at)
VALUES (sqlc.arg(app_id)::uuid, sqlc.arg(node_id)::uuid, sqlc.arg(granted_by_node_id)::uuid, sqlc.arg(expires_at)::timestamptz)
ON CONFLICT (app_id, node_id) DO UPDATE
   SET granted_by_node_id = excluded.granted_by_node_id,
       granted_at = now(),
       expires_at = excluded.expires_at
RETURNING expires_at;

-- name: SecretGrantValid :one
SELECT EXISTS (
    SELECT 1 FROM secret_grants
     WHERE app_id = sqlc.arg(app_id)::uuid
       AND node_id = sqlc.arg(node_id)::uuid
       AND expires_at > sqlc.arg(at)::timestamptz
) AS valid;

-- name: DeleteExpiredSecretGrants :execrows
DELETE FROM secret_grants WHERE expires_at <= sqlc.arg(before)::timestamptz;

-- name: InsertSecretOpenAudit :exec
INSERT INTO secret_open_audit (node_id, app_id, via, entries)
VALUES (sqlc.arg(node_id)::uuid, sqlc.arg(app_id)::uuid, sqlc.arg(via)::text, sqlc.arg(entries)::integer);
