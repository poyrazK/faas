-- adr: 20261018030615
-- name: RecordFunctionMigrationCapture :execrows
UPDATE function_migration_sources SET capture_result=sqlc.arg(capture)::jsonb,phase='prepared'
WHERE instance_id=sqlc.arg(instance_id)::uuid AND lease_token=sqlc.arg(lease_token)::text
 AND phase='preparing' AND capture_token=sqlc.arg(capture_token)::uuid AND capture_result IS NULL;

-- name: GetFunctionMigrationDestinationIdentity :one
SELECT n.vmmd_incarnation,n.vmmd_admission_protocol,
 application_standard_native_input_hash(a.input_snapshot,n.id)::text AS input_hash
FROM compute_nodes n JOIN instance_application_standard_admissions a ON a.instance_id=sqlc.arg(instance_id)::uuid
WHERE n.id=sqlc.arg(node_id)::uuid FOR SHARE OF n,a NOWAIT;

-- name: GetFunctionMigrationDestination :one
SELECT * FROM function_migration_destinations WHERE instance_id=sqlc.arg(instance_id)::uuid AND lease_token=sqlc.arg(lease_token)::text;

-- name: LockFunctionMigrationDestination :one
SELECT * FROM function_migration_destinations WHERE instance_id=sqlc.arg(instance_id)::uuid AND lease_token=sqlc.arg(lease_token)::text FOR UPDATE;

-- name: InsertFunctionMigrationDestination :exec
INSERT INTO function_migration_destinations(token,instance_id,lease_token,node_id,wake_id,phase,grant_data)
VALUES(sqlc.arg(token)::uuid,sqlc.arg(instance_id)::uuid,sqlc.arg(lease_token)::text,sqlc.arg(node_id)::uuid,sqlc.arg(wake_id)::uuid,'issued',sqlc.arg(grant_data)::jsonb);

-- name: SetFunctionMigrationDestination :execrows
UPDATE function_migration_destinations SET phase=sqlc.arg(phase)::text,receipt=sqlc.narg(receipt)::jsonb
WHERE token=sqlc.arg(token)::uuid AND phase=sqlc.arg(expected_phase)::text;

-- adr: 20261018030616
-- name: ListRestartFunctionMigrationDestinations :many
SELECT d.* FROM function_migration_destinations d
JOIN migration_leases m ON m.instance_id=d.instance_id AND m.lease_token=d.lease_token
WHERE d.node_id=sqlc.arg(node_id)::uuid
 AND (sqlc.narg(after_id)::uuid IS NULL OR d.instance_id>sqlc.narg(after_id)::uuid)
 AND (d.phase='committed' OR (d.phase='ready' AND m.lease_expires_at>clock_timestamp()))
ORDER BY d.instance_id LIMIT sqlc.arg(max_rows)::bigint;
