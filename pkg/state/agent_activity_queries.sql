-- name: InsertAgentActivity :exec
-- ADR-20261107220000: one write request of an agent key.
INSERT INTO agent_activity (id, key_id, account_id, org_id, method, path, status, approval_id, reason)
VALUES (sqlc.arg(id)::uuid, sqlc.arg(key_id)::uuid, sqlc.arg(account_id)::uuid, sqlc.narg(org_id)::uuid,
        sqlc.arg(method), sqlc.arg(path), sqlc.arg(status)::int, sqlc.narg(approval_id)::uuid, sqlc.narg(reason));

-- name: ListAgentActivity :many
-- A key's rows since the retention cutoff, newest first, strictly before the
-- (created_at, id) cursor.
SELECT id, key_id, account_id, org_id, method, path, status, approval_id, created_at, reason
FROM agent_activity
WHERE key_id = sqlc.arg(key_id)::uuid
  AND created_at >= sqlc.arg(since)::timestamptz
  AND (created_at, id) < (sqlc.arg(before_at)::timestamptz, sqlc.arg(before_id)::uuid)
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(row_limit)::int;

-- name: DeleteAgentActivityBefore :execrows
-- Rows older than the retention window, a bounded batch at a time.
DELETE FROM agent_activity
WHERE id IN (
    SELECT id FROM agent_activity
    WHERE created_at < sqlc.arg(cutoff)::timestamptz
    LIMIT sqlc.arg(batch)::int
);
