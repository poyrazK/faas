-- ADR-1663: lock current native inputs/instance first, then the parent lease and source.
-- name: LockFunctionMigrationAdmissionLease :one
SELECT * FROM migration_leases
WHERE instance_id=sqlc.arg(instance_id)::uuid AND lease_token=sqlc.arg(lease_token)::text FOR UPDATE;

-- name: SetFunctionMigrationCapturePermission :execrows
UPDATE function_migration_sources SET capture_token=sqlc.arg(capture_token)::uuid, capture_grant=sqlc.arg(capture_grant)::jsonb
WHERE instance_id=sqlc.arg(instance_id)::uuid AND lease_token=sqlc.arg(lease_token)::text
 AND phase='preparing' AND capture_token IS NULL;

-- name: SetFunctionMigrationRecoveryPermission :execrows
UPDATE function_migration_sources SET recovery_grant=sqlc.arg(recovery_grant)::jsonb
WHERE instance_id=sqlc.arg(instance_id)::uuid AND lease_token=sqlc.arg(lease_token)::text
 AND phase='recovering' AND capture_token=sqlc.arg(capture_token)::uuid;
