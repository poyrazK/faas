-- name: GetManagedPostgresCutoverSourceFence :one
SELECT f.*,c.account_id::text AS account_id,c.app_id::text AS app_id,c.state AS cutover_state,
 m.state AS maintenance_state,m.owner_oid,m.database_oid
FROM managed_postgres_cutover_source_fences f JOIN managed_postgres_cutovers c ON c.id=f.cutover_id
JOIN managed_postgres_checkpoint_maintenance m ON m.id=f.maintenance_id
WHERE c.account_id=sqlc.arg(account_id)::text::uuid AND c.id=sqlc.arg(id)::text::uuid;

-- name: LockManagedPostgresCutoverSourceFence :one
SELECT cutover_id FROM managed_postgres_cutover_source_fences WHERE cutover_id=sqlc.arg(id)::text::uuid FOR UPDATE;

-- name: GetManagedPostgresCutoverSourceMaintenance :one
SELECT * FROM managed_postgres_checkpoint_maintenance WHERE source_database_id=sqlc.arg(id)::text::uuid FOR UPDATE;

-- name: InsertManagedPostgresCutoverSourceMaintenance :exec
INSERT INTO managed_postgres_checkpoint_maintenance(id,source_database_id,reserved_by_cutover_id,backend_id,backend_fingerprint,
 source_provider_resource_id,source_data_resource_id)
VALUES(sqlc.arg(id)::text::uuid,sqlc.arg(source_id)::text::uuid,sqlc.arg(cutover_id)::text::uuid,sqlc.arg(backend_id),sqlc.arg(fingerprint),
 sqlc.arg(provider_id),sqlc.arg(data_id));

-- name: InsertManagedPostgresCutoverSourceFence :exec
INSERT INTO managed_postgres_cutover_source_fences(cutover_id,owner_token,maintenance_id,source_database_id,backend_id,backend_fingerprint,
 source_provider_resource_id,source_data_resource_id,source_generation,source_spec,owner_node_id,owner_revision,admission_fenced_at,runtime_fingerprint,
 retry_at,created_at,updated_at)
VALUES(sqlc.arg(id)::text::uuid,sqlc.arg(owner_token)::text::uuid,sqlc.arg(maintenance_id)::text::uuid,sqlc.arg(source_id)::text::uuid,
 sqlc.arg(backend_id),sqlc.arg(fingerprint),sqlc.arg(provider_id),sqlc.arg(data_id),sqlc.arg(generation),sqlc.arg(spec)::jsonb,
 sqlc.arg(node_id)::text::uuid,sqlc.arg(owner_revision),sqlc.arg(fenced_at),sqlc.arg(runtime_fingerprint),sqlc.arg(now),sqlc.arg(now),sqlc.arg(now));

-- name: ClaimManagedPostgresCutoverSourceFence :execrows
UPDATE managed_postgres_cutover_source_fences SET state=sqlc.arg(state),lease_token=sqlc.arg(token),lease_until=sqlc.arg(until)::timestamptz,
 attempt_count=attempt_count+1,updated_at=clock_timestamp()
WHERE cutover_id=sqlc.arg(id)::text::uuid AND state<>'released' AND retry_at<=clock_timestamp()
 AND (lease_until IS NULL OR lease_until<=clock_timestamp()) AND sqlc.arg(until)::timestamptz>clock_timestamp();

-- name: SaveManagedPostgresCutoverSourceFence :execrows
UPDATE managed_postgres_cutover_source_fences SET state=sqlc.arg(state),database_names=sqlc.arg(names)::text[],
 catalog_fingerprint=sqlc.arg(catalog_fingerprint),
 catalog_invalidated_at=sqlc.narg(catalog_invalidated_at)::timestamptz,
 close_requested_at=sqlc.narg(close_requested_at)::timestamptz,closure=sqlc.narg(closure)::jsonb,observed_at=sqlc.narg(observed_at)::timestamptz,
 last_error_code=sqlc.arg(error_code),remote_terminal_state=sqlc.narg(terminal_state)::text,remote_released_at=sqlc.narg(remote_released_at)::timestamptz,
 lease_token=CASE WHEN sqlc.arg(keep_lease)::boolean THEN lease_token ELSE NULL END,
 lease_until=CASE WHEN sqlc.arg(keep_lease)::boolean THEN lease_until ELSE NULL END,
 attempt_count=CASE WHEN sqlc.arg(error_code)::text='' THEN 0 ELSE attempt_count END,
 retry_at=sqlc.arg(retry_at)::timestamptz,updated_at=clock_timestamp()
WHERE cutover_id=sqlc.arg(id)::text::uuid AND lease_token=sqlc.arg(token)::text AND lease_until>clock_timestamp() AND state<>'released';

-- name: SaveManagedPostgresCutoverSourceMaintenance :execrows
UPDATE managed_postgres_checkpoint_maintenance SET state=sqlc.arg(state),owner_oid=sqlc.narg(owner_oid),database_oid=sqlc.narg(database_oid),
 role_requested_at=coalesce(role_requested_at,sqlc.narg(role_requested_at)::timestamptz),
 database_requested_at=coalesce(database_requested_at,sqlc.narg(database_requested_at)::timestamptz),
 activation_requested_at=coalesce(activation_requested_at,sqlc.narg(activation_requested_at)::timestamptz),
 ready_at=coalesce(ready_at,sqlc.narg(ready_at)::timestamptz),updated_at=clock_timestamp()
WHERE id=sqlc.arg(id)::text::uuid AND state=sqlc.arg(expected_state)::text;

-- name: DueManagedPostgresCutoverSourceFences :many
SELECT c.* FROM managed_postgres_cutover_source_fences f JOIN managed_postgres_cutovers c ON c.id=f.cutover_id
WHERE f.state<>'released' AND f.retry_at<=sqlc.arg(now)::timestamptz AND (f.lease_until IS NULL OR f.lease_until<=sqlc.arg(now))
 AND (sqlc.arg(include_pending)::boolean OR f.state<>'pending' OR c.state IN ('cancelling','cancelled'))
ORDER BY f.retry_at,f.cutover_id LIMIT sqlc.arg(batch_size)::integer;

-- name: CountManagedPostgresCutoverSourceBindings :one
SELECT count(*) FROM managed_postgres_bindings WHERE database_id=sqlc.arg(id)::text::uuid AND state<>'deleted';
