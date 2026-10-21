-- ADR-20261018030613: schedd fences rollback before vmmd can reopen a function source.
-- name: RegisterFunctionMigrationSource :execrows
WITH owner AS (
    SELECT i.id, i.wake_id, m.lease_token, m.source_node_id
    FROM migration_leases m JOIN instances i ON i.id = m.instance_id
    WHERE m.instance_id = sqlc.arg(instance_id)::uuid AND m.lease_token = sqlc.arg(lease_token)::text
        AND m.source_node_id = sqlc.arg(source_node_id)::text AND i.node_id::text = m.source_node_id
        AND i.wake_id = sqlc.arg(source_wake_id)::uuid AND i.state = 'running' AND coalesce(i.lease_token, '') = ''
), handles AS (
    UPDATE migration_leases m SET mem_storage_key = sqlc.arg(memory_key)::text, vmstate_storage_key = sqlc.arg(vmstate_key)::text
    FROM owner o WHERE m.instance_id = o.id AND m.lease_token = o.lease_token
    RETURNING m.instance_id, m.lease_token, m.source_node_id
)
INSERT INTO function_migration_sources (instance_id, lease_token, source_node_id, source_wake_id, phase)
SELECT h.instance_id, h.lease_token, h.source_node_id, o.wake_id, 'preparing'
FROM handles h JOIN owner o ON h.instance_id = o.id AND h.lease_token = o.lease_token;

-- name: GetFunctionMigrationSource :one
SELECT * FROM function_migration_sources
WHERE instance_id = sqlc.arg(instance_id)::uuid AND lease_token = sqlc.arg(lease_token)::text;

-- name: GetFunctionMigrationSourceByInstance :one
SELECT * FROM function_migration_sources WHERE instance_id = sqlc.arg(instance_id)::uuid;

-- name: ListFunctionMigrationSourceInstances :many
SELECT s.instance_id FROM function_migration_sources s JOIN instances i ON i.id = s.instance_id
WHERE i.app_id = sqlc.arg(app_id)::uuid;

-- name: LockFunctionMigrationSourceLease :one
SELECT pending, lease_expires_at FROM migration_leases
WHERE instance_id = sqlc.arg(instance_id)::uuid AND lease_token = sqlc.arg(lease_token)::text FOR UPDATE;

-- name: LockFunctionMigrationSource :one
SELECT * FROM function_migration_sources
WHERE instance_id = sqlc.arg(instance_id)::uuid AND lease_token = sqlc.arg(lease_token)::text
FOR UPDATE;

-- name: LockFunctionMigrationSourceNode :exec
SELECT pg_advisory_xact_lock(sqlc.arg(lock_class)::int, hashtext(sqlc.arg(node_id)::text));

-- name: SetFunctionMigrationSourcePhase :execrows
UPDATE function_migration_sources SET phase = sqlc.arg(phase)::text
WHERE instance_id = sqlc.arg(instance_id)::uuid AND lease_token = sqlc.arg(lease_token)::text
    AND phase = sqlc.arg(expected_phase)::text;

-- name: FenceFunctionMigrationSource :execrows
UPDATE instances SET state = 'migrating', lease_token = sqlc.arg(rollback_token)::text,
    migration_started_at = coalesce(migration_started_at, clock_timestamp())
WHERE id = sqlc.arg(instance_id)::uuid AND node_id::text = sqlc.arg(source_node_id)::text
    AND wake_id = sqlc.arg(source_wake_id)::uuid AND state = sqlc.arg(expected_state)::text
    AND coalesce(lease_token, '') = sqlc.arg(expected_token)::text;

-- name: CompleteFunctionMigrationSource :execrows
UPDATE instances SET state = sqlc.arg(state)::text, lease_token = NULL, migration_started_at = NULL
WHERE id = sqlc.arg(instance_id)::uuid AND node_id::text = sqlc.arg(source_node_id)::text
    AND wake_id = sqlc.arg(source_wake_id)::uuid AND state = 'migrating'
    AND lease_token = sqlc.arg(rollback_token)::text;

-- Ownership fencing of an already-MIGRATING source has no new runtime effect.
-- Do not re-admit its stale native inputs merely to prohibit a late commit.
-- name: FencePreparedFunctionMigrationSource :execrows
UPDATE instances SET lease_token=sqlc.arg(rollback_token)::text
WHERE id=sqlc.arg(instance_id)::uuid AND node_id::text=sqlc.arg(source_node_id)::text
 AND wake_id=sqlc.arg(source_wake_id)::uuid AND state='migrating'
 AND coalesce(lease_token,'')=sqlc.arg(expected_token)::text;

-- name: ListExpiredFunctionMigrationSources :many
SELECT s.* FROM function_migration_sources s JOIN migration_leases m
    ON m.instance_id = s.instance_id AND m.lease_token = s.lease_token
WHERE m.lease_expires_at <= sqlc.arg(now)::timestamptz
ORDER BY m.lease_expires_at, s.instance_id LIMIT sqlc.arg(max_rows)::bigint;
