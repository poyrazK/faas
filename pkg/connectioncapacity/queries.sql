-- name: CapacityObserve :one
INSERT INTO postgres_capacity_resources(resource_key,placement,generation,report_sha256,connections,slots,senders,workers,observed_at,expires_at)
VALUES(sqlc.arg(resource_key),sqlc.arg(placement),sqlc.arg(generation),sqlc.arg(report_sha256),sqlc.arg(connections),sqlc.arg(slots),sqlc.arg(senders),sqlc.arg(workers),sqlc.arg(observed_at),sqlc.arg(expires_at))
ON CONFLICT(resource_key) DO UPDATE SET placement=excluded.placement,generation=excluded.generation,report_sha256=excluded.report_sha256,
 connections=excluded.connections,slots=excluded.slots,senders=excluded.senders,workers=excluded.workers,observed_at=excluded.observed_at,expires_at=excluded.expires_at
WHERE postgres_capacity_resources.observed_at<=excluded.observed_at
 AND (postgres_capacity_resources.placement=excluded.placement AND postgres_capacity_resources.generation=excluded.generation
  OR NOT EXISTS(SELECT 1 FROM postgres_capacity_reservations r WHERE r.resource_key=excluded.resource_key))
RETURNING *;

-- name: CapacityLock :exec
SELECT pg_advisory_xact_lock(51733);

-- name: CapacityLockBindingTarget :exec
SELECT d.id FROM managed_postgres_databases d JOIN postgres_capacity_targets t ON t.database_id=d.id
WHERE d.id=sqlc.arg(database_id)::text::uuid AND d.account_id=sqlc.arg(account_id)::text::uuid FOR UPDATE OF d;

-- name: CapacityRecoverSourceHolds :exec
INSERT INTO postgres_capacity_reservations(owner_kind,owner_id,endpoint,resource_key,placement,generation,connections,slots,senders,workers)
SELECT 'pipeline',p.id,'source',s.resource_key,s.placement,s.generation,5,3,3,0
FROM data_pipelines p JOIN postgres_capacity_sources s ON s.connection_id=p.source_connection_id AND s.account_id=p.account_id
WHERE p.state<>'deleted' ON CONFLICT DO NOTHING;

-- name: CapacityRecoverTargetHolds :exec
INSERT INTO postgres_capacity_reservations(owner_kind,owner_id,endpoint,resource_key,placement,generation,connections,slots,senders,workers)
SELECT 'pipeline',p.id,'target',t.resource_key,t.placement,t.generation,5,0,0,3
FROM data_pipelines p JOIN postgres_capacity_targets t ON t.database_id=p.database_id AND t.account_id=p.account_id
WHERE p.state<>'deleted' ON CONFLICT DO NOTHING;

-- name: CapacityUnboundedConsumers :one
SELECT (EXISTS(SELECT 1 FROM managed_postgres_bindings b JOIN postgres_capacity_targets t ON t.database_id=b.database_id
 WHERE t.resource_key=sqlc.arg(resource_key) AND b.state<>'deleted' AND b.connection_limit=0)
 OR EXISTS(SELECT 1 FROM managed_postgres_cutovers c JOIN postgres_capacity_targets t ON t.database_id IN(c.source_database_id,c.target_database_id) WHERE t.resource_key=sqlc.arg(resource_key) AND c.state<>'cancelled')
 OR EXISTS(SELECT 1 FROM data_pipelines p WHERE p.state<>'deleted' AND
 (NOT EXISTS(SELECT 1 FROM postgres_capacity_reservations h WHERE h.owner_kind='pipeline' AND h.owner_id=p.id AND h.endpoint='source') OR
  NOT EXISTS(SELECT 1 FROM postgres_capacity_reservations h WHERE h.owner_kind='pipeline' AND h.owner_id=p.id AND h.endpoint='target'))))::boolean AS unbounded;

-- name: CapacityMapSource :exec
INSERT INTO postgres_capacity_sources(connection_id,account_id,connection_revision,resource_key,placement,generation)
VALUES(sqlc.arg(connection_id)::text::uuid,sqlc.arg(account_id)::text::uuid,sqlc.arg(connection_revision),sqlc.arg(resource_key),sqlc.arg(placement),sqlc.arg(generation))
ON CONFLICT(connection_id) DO UPDATE SET connection_revision=excluded.connection_revision,resource_key=excluded.resource_key,placement=excluded.placement,generation=excluded.generation
WHERE postgres_capacity_sources.account_id=excluded.account_id AND postgres_capacity_sources.connection_revision<=excluded.connection_revision;

-- name: CapacityMapTarget :exec
INSERT INTO postgres_capacity_targets(database_id,account_id,database_generation,backend_fingerprint,data_resource_id,resource_key,placement,generation)
VALUES(sqlc.arg(database_id)::text::uuid,sqlc.arg(account_id)::text::uuid,sqlc.arg(database_generation),sqlc.arg(backend_fingerprint),sqlc.arg(data_resource_id),sqlc.arg(resource_key),sqlc.arg(placement),sqlc.arg(generation))
ON CONFLICT(database_id) DO UPDATE SET database_generation=excluded.database_generation,backend_fingerprint=excluded.backend_fingerprint,data_resource_id=excluded.data_resource_id,
 resource_key=excluded.resource_key,placement=excluded.placement,generation=excluded.generation
WHERE postgres_capacity_targets.account_id=excluded.account_id AND postgres_capacity_targets.database_generation<=excluded.database_generation;

-- name: CapacityRead :one
SELECT r.*,coalesce(sum(h.connections),0)::bigint AS reserved_connections,coalesce(sum(h.slots),0)::bigint AS reserved_slots,
 coalesce(sum(h.senders),0)::bigint AS reserved_senders,coalesce(sum(h.workers),0)::bigint AS reserved_workers
FROM postgres_capacity_resources r LEFT JOIN postgres_capacity_reservations h ON h.resource_key=r.resource_key
WHERE r.resource_key=sqlc.arg(resource_key) GROUP BY r.resource_key;

-- name: CapacityTarget :one
SELECT * FROM postgres_capacity_targets WHERE database_id=sqlc.arg(database_id)::text::uuid AND account_id=sqlc.arg(account_id)::text::uuid;

-- name: CapacitySource :one
SELECT * FROM postgres_capacity_sources WHERE connection_id=sqlc.arg(connection_id)::text::uuid AND account_id=sqlc.arg(account_id)::text::uuid;

-- name: CapacityPipelineHolds :many
SELECT h.* FROM postgres_capacity_reservations h JOIN data_pipelines p ON p.id=h.owner_id
WHERE h.owner_kind='pipeline' AND p.id=sqlc.arg(id)::text::uuid AND p.account_id=sqlc.arg(account_id)::text::uuid ORDER BY h.endpoint;
