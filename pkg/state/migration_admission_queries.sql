-- name: ReadMigrationAdmission :one
SELECT * FROM migration_admissions WHERE id=sqlc.arg(id)::uuid;

-- name: LockMigrationAdmission :one
SELECT * FROM migration_admissions WHERE id=sqlc.arg(id)::uuid FOR UPDATE;

-- name: InsertMigrationAdmission :one
INSERT INTO migration_admissions(id,instance_id,source_node_id,destination_node_id,source_wake_id,destination_wake_id,lease_token,mem_storage_key,vmstate_storage_key,request_digest,destination_generation,ram_mb,cpu_millicores,vcpu,expires_at)
VALUES(sqlc.arg(id)::uuid,sqlc.arg(instance_id)::uuid,sqlc.arg(source_node_id)::uuid,sqlc.arg(destination_node_id)::uuid,sqlc.arg(source_wake_id)::uuid,sqlc.arg(destination_wake_id)::uuid,sqlc.arg(lease_token)::text,sqlc.arg(mem_storage_key)::text,sqlc.arg(vmstate_storage_key)::text,sqlc.arg(request_digest)::text,sqlc.arg(destination_generation)::bigint,sqlc.arg(ram_mb)::integer,sqlc.arg(cpu_millicores)::integer,sqlc.arg(vcpu)::integer,sqlc.arg(expires_at)::timestamptz) RETURNING *;

-- name: LockMigrationAdmissionNode :exec
SELECT pg_advisory_xact_lock(1852793957,hashtext(sqlc.arg(node_id)::text));

-- name: StartMigrationAdmission :execrows
UPDATE migration_admissions SET phase='adopting' WHERE id=sqlc.arg(id)::uuid AND phase='reserved';

-- name: AcknowledgeMigrationAdoption :execrows
UPDATE migration_admissions SET phase='adopted',adoption_receipt=sqlc.arg(receipt)::bytea WHERE id=sqlc.arg(id)::uuid AND phase='adopting';

-- name: SetMigrationAdmissionCleaning :execrows
UPDATE migration_admissions SET phase='cleaning' WHERE id=sqlc.arg(id)::uuid AND phase IN ('adopting','adopted');

-- name: SetMigrationAdmissionCleaned :execrows
UPDATE migration_admissions SET phase='cleaned',cleanup_proof=sqlc.arg(proof)::text WHERE id=sqlc.arg(id)::uuid AND phase IN ('reserved','cleaning');

-- name: ListMigrationAdmissionsForRecovery :many
SELECT r.* FROM migration_admissions r LEFT JOIN compute_nodes n ON n.id=r.destination_node_id
WHERE r.destination_node_id=sqlc.arg(node_id)::uuid AND r.phase NOT IN ('cleaned','committed')
AND (sqlc.arg(after_id)::text='' OR r.id>sqlc.arg(after_id)::text::uuid)
AND (r.expires_at<=sqlc.arg(now_at)::timestamptz OR r.phase='cleaning' OR n.id IS NULL OR n.lifecycle NOT IN ('active','recovering') OR n.lifecycle_generation<>r.destination_generation)
ORDER BY r.id LIMIT sqlc.arg(page_limit)::integer;

-- name: MigrationSourceMayResume :one
SELECT (NOT EXISTS(SELECT 1 FROM migration_admissions WHERE instance_id=sqlc.arg(instance_id)::uuid AND lease_token=sqlc.arg(lease_token)::text AND phase<>'cleaned')
 AND NOT EXISTS(SELECT 1 FROM migration_sources WHERE instance_id=sqlc.arg(instance_id)::uuid AND lease_token=sqlc.arg(lease_token)::text AND phase<>'bound'))::boolean;

-- name: MigrationAdmissionPendingForInstance :one
SELECT EXISTS(SELECT 1 FROM migration_admissions WHERE instance_id=sqlc.arg(instance_id)::uuid AND lease_token=sqlc.arg(lease_token)::text AND phase NOT IN ('cleaned','committed'))::boolean;

-- name: MigrationAdmissionNodePending :one
SELECT EXISTS(SELECT 1 FROM migration_admissions WHERE destination_node_id=sqlc.arg(node_id)::uuid AND phase NOT IN ('cleaned','committed'))::boolean;

-- name: LockMigrationSourceResume :one
SELECT instance_id FROM migration_leases WHERE instance_id=sqlc.arg(instance_id)::uuid AND lease_token=sqlc.arg(lease_token)::text FOR UPDATE;

-- name: FenceMigrationSourceResume :exec
UPDATE migration_leases SET resume_fenced=true WHERE instance_id=sqlc.arg(instance_id)::uuid AND lease_token=sqlc.arg(lease_token)::text;

-- name: MigrationAdmissionNodeResources :many
SELECT usage.node_id::text AS node_id,sum(usage.ram_mb)::bigint AS ram_mb,sum(usage.cpu_millicores)::bigint AS cpu_millicores FROM (
 SELECT destination_node_id AS node_id,ram_mb::bigint,cpu_millicores::bigint FROM migration_admissions
 WHERE destination_node_id=ANY(sqlc.arg(node_ids)::uuid[]) AND phase NOT IN ('cleaned','committed')
 UNION ALL
 SELECT n.id,u.ram,u.cpu FROM compute_nodes n CROSS JOIN LATERAL migration_source_node_usage(n.id,true) u WHERE n.id=ANY(sqlc.arg(node_ids)::uuid[])
 UNION ALL
 SELECT i.node_id,greatest(0,i.capacity_ram_mb-i.ram_mb-p.overhead_mb)::bigint,
  greatest(0,i.capacity_cpu_millicores-greatest(coalesce(nullif(a.cpu_millicores,0),p.startup_cpu),CASE WHEN i.startup_cpu_boost_until>now() THEN p.startup_cpu ELSE 0 END))::bigint
 FROM instances i
 LEFT JOIN apps a ON a.id=i.app_id CROSS JOIN service_capacity_policy p
 WHERE p.singleton AND (EXISTS(SELECT 1 FROM migration_admissions r WHERE r.phase='committed' AND r.instance_id=i.id AND r.destination_node_id=i.node_id AND r.destination_wake_id=i.wake_id) OR EXISTS(SELECT 1 FROM migration_sources s WHERE s.phase='resumed' AND s.instance_id=i.id AND s.source_node_id=i.node_id AND s.source_wake_id=i.wake_id)) AND i.node_id=ANY(sqlc.arg(node_ids)::uuid[]) AND i.state IN ('waking','cold_booting','running','draining','warm')
) usage GROUP BY usage.node_id;

-- name: MigrationAdmissionResidentRAM :one
SELECT (coalesce(sum(greatest(0,i.capacity_ram_mb-i.ram_mb-p.overhead_mb)),0)+(SELECT ram FROM migration_source_node_usage(sqlc.arg(node_id)::uuid,true)))::bigint
FROM instances i
CROSS JOIN service_capacity_policy p
WHERE p.singleton AND (EXISTS(SELECT 1 FROM migration_admissions r WHERE r.phase='committed' AND r.instance_id=i.id AND r.destination_node_id=i.node_id AND r.destination_wake_id=i.wake_id) OR EXISTS(SELECT 1 FROM migration_sources s WHERE s.phase='resumed' AND s.instance_id=i.id AND s.source_node_id=i.node_id AND s.source_wake_id=i.wake_id)) AND i.node_id=sqlc.arg(node_id)::uuid AND i.state IN ('waking','cold_booting','running','draining','warm');
