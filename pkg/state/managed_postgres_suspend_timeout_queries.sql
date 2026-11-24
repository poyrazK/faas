-- Customer-chosen scale-to-zero suspend timeouts (gregale postgres
-- suspend-timeout). No row is the platform default. apid checks the caller owns
-- the database before writing.

-- name: SetManagedPostgresSuspendTimeout :exec
INSERT INTO managed_postgres_suspend_timeouts (database_id, seconds, updated_at)
VALUES (sqlc.arg(database_id)::uuid, sqlc.arg(seconds)::integer, now())
ON CONFLICT (database_id) DO UPDATE SET seconds = EXCLUDED.seconds, updated_at = EXCLUDED.updated_at;

-- name: ClearManagedPostgresSuspendTimeout :exec
DELETE FROM managed_postgres_suspend_timeouts WHERE database_id = sqlc.arg(database_id)::uuid;

-- name: ManagedPostgresSuspendTimeouts :many
SELECT t.database_id, t.seconds FROM managed_postgres_suspend_timeouts t
WHERE t.database_id = ANY(sqlc.arg(database_ids)::uuid[]);
