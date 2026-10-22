-- name: LockManagedPostgresDrainReservation :one
SELECT a.id::text AS app_id,i.deployment_id::text AS deployment_id,i.id::text AS instance_id,
 i.node_id::text AS node_id,i.wake_id::text AS wake_id,a.node_id::text AS owner_node_id,
 a.scaling_policy_revision AS owner_revision,a.managed_postgres_admission_fenced_at AS fenced_at
FROM apps a JOIN managed_postgres_cutovers c ON c.app_id=a.id JOIN instances i ON i.app_id=a.id
WHERE c.id=sqlc.arg(cutover_id)::text::uuid AND i.id=sqlc.arg(instance_id)::text::uuid
 AND a.node_id=sqlc.arg(owner_node_id)::text::uuid AND a.managed_postgres_admission_cutover_id=c.id
 AND a.status IN ('active','evicted_cold') AND c.state IN ('verified','verifying','cancelling')
 AND i.kind='wake' AND i.state IN ('waking','cold_booting','running','draining','snapshotting','warm','frozen','failed','stopped')
 AND i.migrated_from_node_id IS NULL
 AND NOT EXISTS (SELECT 1 FROM environment_qualification_executions e WHERE e.instance_id=i.id AND e.retired_at IS NULL)
FOR UPDATE OF a,c,i;

-- name: InsertManagedPostgresDrain :one
INSERT INTO managed_postgres_cutover_drains (id,cutover_id,app_id,deployment_id,instance_id,node_id,wake_id,owner_node_id,owner_revision,fenced_at)
VALUES (sqlc.arg(id)::text::uuid,sqlc.arg(cutover_id)::text::uuid,sqlc.arg(app_id)::text::uuid,sqlc.arg(deployment_id)::text::uuid,
 sqlc.arg(instance_id)::text::uuid,sqlc.arg(node_id)::text::uuid,sqlc.arg(wake_id)::text::uuid,sqlc.arg(owner_node_id)::text::uuid,
 sqlc.arg(owner_revision),sqlc.arg(fenced_at))
ON CONFLICT (cutover_id,instance_id,node_id,wake_id,owner_node_id,owner_revision,fenced_at)
 DO UPDATE SET id=managed_postgres_cutover_drains.id RETURNING *;

-- name: GetManagedPostgresDrain :one
SELECT * FROM managed_postgres_cutover_drains WHERE id=sqlc.arg(id)::text::uuid;

-- name: LockManagedPostgresDrainApp :one
SELECT a.node_id::text AS owner_node_id,a.scaling_policy_revision AS owner_revision,
 a.managed_postgres_admission_cutover_id::text AS cutover_id,a.managed_postgres_admission_fenced_at AS fenced_at
FROM apps a JOIN managed_postgres_cutovers c ON c.id=a.managed_postgres_admission_cutover_id AND c.app_id=a.id
WHERE a.id=sqlc.arg(app_id)::text::uuid AND a.status IN ('active','evicted_cold')
 AND c.state IN ('verified','verifying','cancelling') FOR SHARE OF a,c;

-- name: LockManagedPostgresDrainMember :one
SELECT * FROM managed_postgres_cutover_drains WHERE id=sqlc.arg(id)::text::uuid FOR UPDATE;

-- name: LockManagedPostgresDrainInstance :one
SELECT app_id::text AS app_id,deployment_id::text AS deployment_id,node_id::text AS node_id,wake_id::text AS wake_id,state
FROM instances WHERE id=sqlc.arg(instance_id)::text::uuid AND kind='wake' AND state<>'migrating'
 AND migrated_from_node_id IS NULL
 AND NOT EXISTS (SELECT 1 FROM environment_qualification_executions e WHERE e.instance_id=instances.id AND e.retired_at IS NULL) FOR UPDATE;

-- name: SaveManagedPostgresDrainProof :execrows
UPDATE managed_postgres_cutover_drains SET state='retired',native_generation=sqlc.arg(native_generation)::text::uuid,
 kernel_boot_id=sqlc.arg(kernel_boot_id)::text::uuid,retired_at=clock_timestamp()
WHERE id=sqlc.arg(id)::text::uuid AND state='pending';

-- name: StopManagedPostgresDrainInstance :execrows
UPDATE instances SET state='stopped',terminal_at=coalesce(terminal_at,clock_timestamp())
WHERE id=sqlc.arg(instance_id)::text::uuid AND app_id=sqlc.arg(app_id)::text::uuid
 AND deployment_id=sqlc.arg(deployment_id)::text::uuid AND node_id=sqlc.arg(node_id)::text::uuid AND wake_id=sqlc.arg(wake_id)::text::uuid
 AND state<>'migrating' AND migrated_from_node_id IS NULL;

-- name: ListPendingManagedPostgresDrains :many
SELECT d.* FROM managed_postgres_cutover_drains d JOIN apps a ON a.id=d.app_id
WHERE d.owner_node_id=sqlc.arg(owner_node_id)::text::uuid AND d.state='pending'
 AND a.node_id=d.owner_node_id AND a.scaling_policy_revision=d.owner_revision
 AND a.managed_postgres_admission_cutover_id=d.cutover_id AND a.managed_postgres_admission_fenced_at=d.fenced_at
 AND (sqlc.arg(after_id)::text='' OR d.id>sqlc.arg(after_id)::text::uuid)
ORDER BY d.id LIMIT sqlc.arg(page_limit);

-- name: ReleaseManagedPostgresDrainHostPorts :exec
DELETE FROM container_host_port_leases WHERE node_id=sqlc.arg(node_id)::text::uuid AND instance_id=sqlc.arg(instance_id)::text::uuid;
