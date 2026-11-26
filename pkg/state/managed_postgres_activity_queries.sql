-- Why a managed PostgreSQL database is awake (gregale postgres activity,
-- ADR-20261125231222). apid is the only writer.

-- name: RecordManagedPostgresComputeStart :one
-- Records one start of the database's compute with its likely cause: the
-- first match of app_wake, cron, request, app_start, running_app over the
-- apps bound to the database at the start, else external. A start already
-- recorded is left alone; it reports whether it recorded this one.
WITH db AS (
    SELECT d.id, d.account_id FROM managed_postgres_databases d WHERE d.id = sqlc.arg(database_id)::uuid
), bound AS (
    SELECT DISTINCT b.app_id FROM managed_postgres_bindings b
    WHERE b.database_id = sqlc.arg(database_id)::uuid
      AND b.created_at <= sqlc.arg(started_at)::timestamptz
      AND (b.deleted_at IS NULL OR b.deleted_at > sqlc.arg(started_at)::timestamptz)
), matches AS (
    SELECT 1 AS rank, 'app_wake'::text AS cause, NULL::uuid AS app_id, ''::text AS detail, w.claimed_at AS at
    FROM managed_postgres_app_wake_starts w
    WHERE w.database_id = sqlc.arg(database_id)::uuid
      AND w.claimed_at BETWEEN sqlc.arg(started_at)::timestamptz - interval '30 seconds'
                           AND sqlc.arg(started_at)::timestamptz + interval '5 seconds'
    UNION ALL
    SELECT 2, 'cron', (e.data->>'app_id')::uuid, left(coalesce(e.data->>'path', ''), 512), e.at
    FROM events e
    WHERE e.data ? 'app_id' AND (e.data->>'app_id') IN (SELECT app_id::text FROM bound)
      AND e.kind = 'cron.fired'
      AND e.at BETWEEN sqlc.arg(started_at)::timestamptz - interval '2 minutes'
                   AND sqlc.arg(started_at)::timestamptz + interval '5 seconds'
    UNION ALL
    SELECT CASE WHEN coalesce(e.data->>'request_id', '') <> '' THEN 3 ELSE 4 END,
           CASE WHEN coalesce(e.data->>'request_id', '') <> '' THEN 'request' ELSE 'app_start' END,
           (e.data->>'app_id')::uuid, '', e.at
    FROM events e
    WHERE e.data ? 'app_id' AND (e.data->>'app_id') IN (SELECT app_id::text FROM bound)
      AND e.kind = 'wake.admitted'
      AND e.at BETWEEN sqlc.arg(started_at)::timestamptz - interval '1 minute'
                   AND sqlc.arg(started_at)::timestamptz + interval '5 seconds'
    UNION ALL
    SELECT 5, 'running_app', i.app_id, '', i.started_at
    FROM instances i
    WHERE i.app_id IN (SELECT app_id FROM bound) AND i.kind = 'wake'
      AND i.started_at <= sqlc.arg(started_at)::timestamptz
      AND (i.terminal_at IS NULL OR i.terminal_at >= sqlc.arg(started_at)::timestamptz)
      AND NOT (i.state IN ('pending', 'parked', 'frozen') AND coalesce(i.parked_at, i.started_at) <= sqlc.arg(started_at)::timestamptz)
    UNION ALL
    SELECT 6, 'external', NULL::uuid, '', NULL::timestamptz
), best AS (
    SELECT cause, app_id, detail FROM matches ORDER BY rank, at DESC NULLS LAST LIMIT 1
)
INSERT INTO managed_postgres_compute_starts
    (database_id, account_id, provider_operation_id, started_at, cause, cause_app_id, cause_detail)
SELECT db.id, db.account_id, sqlc.arg(provider_operation_id)::text, sqlc.arg(started_at)::timestamptz,
       best.cause, best.app_id, best.detail
FROM db CROSS JOIN best
ON CONFLICT (database_id, provider_operation_id) DO NOTHING
RETURNING true AS recorded;

-- name: RecordManagedPostgresComputeSuspend :execrows
-- Closes the database's latest start at or before the suspend, if it is
-- still open.
UPDATE managed_postgres_compute_starts s SET suspended_at = sqlc.arg(suspended_at)::timestamptz
WHERE s.database_id = sqlc.arg(database_id)::uuid AND s.suspended_at IS NULL
  AND s.started_at = (
      SELECT max(o.started_at) FROM managed_postgres_compute_starts o
      WHERE o.database_id = sqlc.arg(database_id)::uuid AND o.started_at <= sqlc.arg(suspended_at)::timestamptz);

-- A start's end is its suspend; one whose suspend was never read ends at
-- the database's next recorded start (a compute runs once at a time), and
-- only the latest open start runs to now.

-- name: PruneManagedPostgresComputeStarts :execrows
-- Deletes up to batch_size starts recorded before the cutoff.
DELETE FROM managed_postgres_compute_starts s
WHERE (s.database_id, s.provider_operation_id) IN (
    SELECT o.database_id, o.provider_operation_id FROM managed_postgres_compute_starts o
    WHERE o.started_at < sqlc.arg(cutoff)::timestamptz
    LIMIT sqlc.arg(batch_size)::int);

-- name: PruneManagedPostgresAppWakeStarts :execrows
-- Deletes up to batch_size wake-with-app claims made before the cutoff.
DELETE FROM managed_postgres_app_wake_starts w
WHERE (w.database_id, w.claimed_at) IN (
    SELECT o.database_id, o.claimed_at FROM managed_postgres_app_wake_starts o
    WHERE o.claimed_at < sqlc.arg(cutoff)::timestamptz
    LIMIT sqlc.arg(batch_size)::int);

-- name: ListManagedPostgresComputeStarts :many
-- The database's starts awake at or after since, newest first.
SELECT s.provider_operation_id, s.started_at, s.suspended_at, s.cause, s.cause_app_id, s.cause_detail,
       coalesce(a.slug, '')::text AS cause_app_slug
FROM managed_postgres_compute_starts s
LEFT JOIN apps a ON a.id = s.cause_app_id
WHERE s.database_id = sqlc.arg(database_id)::uuid
  AND coalesce(s.suspended_at, (SELECT min(n.started_at) FROM managed_postgres_compute_starts n WHERE n.database_id = s.database_id AND n.started_at > s.started_at), 'infinity'::timestamptz) > sqlc.arg(since)::timestamptz
ORDER BY s.started_at DESC
LIMIT sqlc.arg(row_limit)::int;

-- name: ManagedPostgresComputeStartCauses :many
-- The database's starts awake in [since, now] grouped by cause, with the
-- awake seconds inside it, most starts first.
WITH spans AS (
    SELECT s.cause, s.cause_app_id, s.cause_detail, s.started_at,
           least(coalesce(s.suspended_at, (SELECT min(n.started_at) FROM managed_postgres_compute_starts n WHERE n.database_id = s.database_id AND n.started_at > s.started_at), sqlc.arg(now)::timestamptz), sqlc.arg(now)::timestamptz) AS ended_at
    FROM managed_postgres_compute_starts s
    WHERE s.database_id = sqlc.arg(database_id)::uuid AND s.started_at < sqlc.arg(now)::timestamptz
)
SELECT p.cause, p.cause_app_id, p.cause_detail, coalesce(a.slug, '')::text AS cause_app_slug,
       count(*)::int AS starts,
       coalesce(sum(extract(epoch FROM (p.ended_at - greatest(p.started_at, sqlc.arg(since)::timestamptz)))), 0)::bigint AS awake_seconds,
       min(p.started_at)::timestamptz AS first_started_at
FROM spans p
LEFT JOIN apps a ON a.id = p.cause_app_id
WHERE p.ended_at > sqlc.arg(since)::timestamptz
GROUP BY p.cause, p.cause_app_id, p.cause_detail, a.slug
ORDER BY starts DESC, awake_seconds DESC;

-- name: ManagedPostgresAccountComputeStarts :many
-- Each of the account's databases' starts awake in [since, now] and their
-- awake seconds inside it.
WITH spans AS (
    SELECT s.database_id, s.started_at,
           least(coalesce(s.suspended_at, (SELECT min(n.started_at) FROM managed_postgres_compute_starts n WHERE n.database_id = s.database_id AND n.started_at > s.started_at), sqlc.arg(now)::timestamptz), sqlc.arg(now)::timestamptz) AS ended_at
    FROM managed_postgres_compute_starts s
    WHERE s.account_id = sqlc.arg(account_id)::uuid AND s.started_at < sqlc.arg(now)::timestamptz
)
SELECT p.database_id, count(*)::int AS starts,
       coalesce(sum(extract(epoch FROM (p.ended_at - greatest(p.started_at, sqlc.arg(since)::timestamptz)))), 0)::bigint AS awake_seconds
FROM spans p
WHERE p.ended_at > sqlc.arg(since)::timestamptz
GROUP BY p.database_id;

-- name: ManagedPostgresDatabaseComputeUnitSeconds :many
-- Each of the account's databases' compute unit seconds in usage windows
-- starting at or after since.
SELECT u.database_id, coalesce(sum(u.quantity), 0)::bigint AS compute_unit_seconds
FROM managed_postgres_usage u
WHERE u.account_id = sqlc.arg(account_id)::uuid AND u.meter = 'compute_unit_seconds'
  AND u.window_from >= sqlc.arg(since)::timestamptz
GROUP BY u.database_id;
