-- name: GetManagedPostgresBindingTimeoutPolicy :one
SELECT * FROM native_postgres_binding_timeouts WHERE binding_id = @binding_id;

-- name: LockManagedPostgresBindingTimeoutEdit :one
SELECT state = 'ready' AND rotation_previous_generation IS NULL AS ready
FROM managed_postgres_bindings
WHERE id = @binding_id AND account_id = @account_id FOR UPDATE;

-- name: SaveManagedPostgresBindingTimeoutPolicy :one
INSERT INTO native_postgres_binding_timeouts
    (binding_id, statement_timeout_ms, lock_timeout_ms, idle_in_transaction_session_timeout_ms, revision)
VALUES (@binding_id, @statement_timeout_ms, @lock_timeout_ms, @idle_in_transaction_session_timeout_ms, 1)
ON CONFLICT (binding_id) DO UPDATE SET
    statement_timeout_ms = EXCLUDED.statement_timeout_ms,
    lock_timeout_ms = EXCLUDED.lock_timeout_ms,
    idle_in_transaction_session_timeout_ms = EXCLUDED.idle_in_transaction_session_timeout_ms,
    revision = native_postgres_binding_timeouts.revision + 1,
    applied_at = NULL,
    retry_at = clock_timestamp()
RETURNING revision;

-- name: DueManagedPostgresBindingTimeoutPolicies :many
SELECT b.* FROM native_postgres_binding_timeouts p
JOIN managed_postgres_bindings b ON b.id = p.binding_id
WHERE p.applied_at IS NULL AND p.retry_at <= now()
  AND b.state = 'ready' AND b.rotation_previous_generation IS NULL
ORDER BY p.retry_at, b.id LIMIT @batch_size;

-- name: LockManagedPostgresBindingTimeoutApply :one
SELECT b.provider_identity_id
FROM native_postgres_binding_timeouts p
JOIN managed_postgres_bindings b ON b.id = p.binding_id
WHERE p.applied_at IS NULL AND p.retry_at <= now()
  AND b.id = @binding_id AND b.state = 'ready'
  AND b.rotation_previous_generation IS NULL AND b.credential_generation = @credential_generation
FOR UPDATE OF b SKIP LOCKED;

-- name: RetryManagedPostgresBindingTimeoutPolicy :exec
UPDATE native_postgres_binding_timeouts
SET retry_at = clock_timestamp() + interval '30 seconds' WHERE binding_id = @binding_id;

-- name: ApplyManagedPostgresBindingTimeoutPolicy :exec
UPDATE native_postgres_binding_timeouts SET applied_at = clock_timestamp() WHERE binding_id = @binding_id;

-- name: PublishDatabaseRolePoolRevision :execrows
UPDATE database_role_verifiers SET pool_revision = @revision
WHERE database_id = @database_id AND role_name = @role_name;

-- name: GetDatabaseRolePoolRevisions :many
SELECT database_id, role_name, pool_revision FROM database_role_verifiers
WHERE database_id = ANY(@database_ids::uuid[]);
