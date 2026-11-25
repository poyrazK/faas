-- Managed PostgreSQL databases that start when a bound app wakes (gregale
-- postgres app-wake, ADR-20261125105038). No row is off. apid checks the
-- caller owns the database before writing.

-- name: EnableManagedPostgresWakeWithApp :exec
INSERT INTO managed_postgres_wake_with_app (database_id)
VALUES (sqlc.arg(database_id)::uuid)
ON CONFLICT (database_id) DO NOTHING;

-- name: DisableManagedPostgresWakeWithApp :exec
DELETE FROM managed_postgres_wake_with_app WHERE database_id = sqlc.arg(database_id)::uuid;

-- name: ManagedPostgresWakeWithApp :many
SELECT w.database_id FROM managed_postgres_wake_with_app w
WHERE w.database_id = ANY(sqlc.arg(database_ids)::uuid[]);

-- name: ManagedPostgresWakeWithAppApps :many
-- The apps with a live binding to an opted-in database: the only apps whose
-- wakes can start one.
SELECT DISTINCT b.app_id FROM managed_postgres_wake_with_app w
JOIN managed_postgres_bindings b ON b.database_id = w.database_id
WHERE b.deleted_at IS NULL AND b.state = 'ready';

-- name: ClaimManagedPostgresWakeStarts :many
-- Claims the opted-in, ready, scale-to-zero databases bound to the app that
-- were not started in the last gap, and stamps them, so only the claiming
-- apid replica starts each one.
UPDATE managed_postgres_wake_with_app w SET last_started_at = now()
FROM managed_postgres_databases d
WHERE w.database_id = d.id
  AND d.deleted_at IS NULL AND d.state = 'ready' AND d.scale_to_zero
  AND d.provider_resource_id IS NOT NULL
  AND EXISTS (SELECT 1 FROM managed_postgres_bindings b
      WHERE b.database_id = d.id AND b.app_id = sqlc.arg(app_id)::uuid
        AND b.deleted_at IS NULL AND b.state = 'ready')
  AND (w.last_started_at IS NULL
       OR w.last_started_at < now() - make_interval(secs => sqlc.arg(gap_seconds)::double precision))
RETURNING d.id, d.backend_id, d.backend_fingerprint, d.provider_resource_id::text AS provider_resource_id,
  w.last_started_at::timestamptz AS claimed_at;

-- name: DeferManagedPostgresWakeStart :exec
-- Moves back the stamp of a claim that started nothing, by shift seconds, so
-- the next wake may claim it sooner; a later claim's stamp is left alone.
UPDATE managed_postgres_wake_with_app
SET last_started_at = last_started_at - make_interval(secs => sqlc.arg(shift_seconds)::double precision)
WHERE database_id = sqlc.arg(database_id)::uuid
  AND last_started_at = sqlc.arg(claimed_at)::timestamptz;
