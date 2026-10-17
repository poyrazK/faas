-- name: ReadMigrationSource :one
SELECT * FROM migration_sources WHERE lease_token=sqlc.arg(lease_token)::text;

-- name: RecordMigrationSourceSnapshot :execrows
UPDATE migration_leases l SET mem_storage_key=sqlc.arg(memory_key)::text,vmstate_storage_key=sqlc.arg(state_key)::text
WHERE l.lease_token=sqlc.arg(lease_token)::text AND NOT l.resume_fenced AND l.lease_expires_at>clock_timestamp()
AND (l.mem_storage_key='' OR l.mem_storage_key=sqlc.arg(memory_key)::text) AND (l.vmstate_storage_key='' OR l.vmstate_storage_key=sqlc.arg(state_key)::text)
AND EXISTS(SELECT 1 FROM migration_sources r WHERE r.lease_token=l.lease_token AND r.source_node_id=sqlc.arg(node_id)::uuid AND r.phase='bound');

-- name: LockMigrationSource :one
SELECT * FROM migration_sources WHERE lease_token=sqlc.arg(lease_token)::text FOR UPDATE;

-- name: InsertMigrationSource :one
INSERT INTO migration_sources(lease_token,instance_id,source_node_id,source_wake_id,native_generation,physical_binding,ram_mb,cpu_millicores,vcpu)
SELECT sqlc.arg(lease_token)::text,i.id,sqlc.arg(node_id)::uuid,sqlc.arg(wake_id)::uuid,sqlc.arg(generation)::uuid,sqlc.arg(binding)::text,
 greatest(i.capacity_ram_mb,i.ram_mb+p.overhead_mb),
 greatest(i.capacity_cpu_millicores,coalesce(nullif(a.cpu_millicores,0),p.startup_cpu)),
 greatest(i.capacity_vcpu,CASE WHEN i.app_id IS NULL THEN 1 ELSE coalesce((p.plan_vcpus->>ac.plan)::integer,4) END)
FROM instances i LEFT JOIN apps a ON a.id=i.app_id LEFT JOIN accounts ac ON ac.id=a.account_id CROSS JOIN service_capacity_policy p
WHERE p.singleton AND i.id=sqlc.arg(instance_id)::uuid RETURNING *;

-- name: SetMigrationSourceRetiring :execrows
UPDATE migration_sources SET phase='retiring' WHERE lease_token=sqlc.arg(lease_token)::text AND phase='bound';

-- name: AcknowledgeMigrationSource :execrows
UPDATE migration_sources SET phase=sqlc.arg(phase)::text,proof=sqlc.arg(proof)::text WHERE lease_token=sqlc.arg(lease_token)::text AND phase IN ('bound','retiring');

-- name: ListMigrationSourcesForRecovery :many
SELECT r.* FROM migration_sources r LEFT JOIN migration_leases l ON l.lease_token=r.lease_token
WHERE r.source_node_id=sqlc.arg(node_id)::uuid AND r.lease_token>sqlc.arg(after_token)::text AND r.phase IN ('bound','retiring')
AND (r.phase='retiring' OR l.lease_expires_at<=sqlc.arg(now_at)::timestamptz OR l.lease_token IS NULL)
ORDER BY r.lease_token LIMIT sqlc.arg(page_limit)::integer;

-- name: ListMigrationSourceLeasesForRecovery :many
SELECT l.* FROM migration_leases l LEFT JOIN migration_sources r ON r.lease_token=l.lease_token
WHERE l.source_node_id=sqlc.arg(node_id)::uuid::text AND l.lease_token>sqlc.arg(after_token)::text
AND (l.lease_expires_at<=sqlc.arg(now_at)::timestamptz OR l.resume_fenced OR r.phase IN ('retiring','retired','resumed'))
ORDER BY l.lease_token LIMIT sqlc.arg(page_limit)::integer;
