-- ADR-20261031110037: managed PostgreSQL overage charges delivered per
-- billing provider, usage subject and UTC month.

-- name: ManagedPostgresOverageSubjects :many
-- Subjects that may owe overage for the month: those that reached the
-- compute allowance (as the cap sweep recorded it), those with overage on,
-- whose storage past the pool is billed (ADR-20261031110037 amendment 2), and
-- those already charged or with a reserved step awaiting reconciliation.
SELECT s.subject_id, max(s.allowance_unit_seconds)::bigint AS allowance_unit_seconds,
    bool_or(s.overage_enabled) AS overage_enabled
FROM (
    SELECT e.subject_id, e.allowance_unit_seconds, e.overage_enabled
    FROM managed_postgres_cap_events e
    WHERE e.period_start = sqlc.arg(period_start)::timestamptz AND e.threshold = 'allowance'
    UNION ALL
    SELECT o.subject_id, 0::bigint, true
    FROM managed_postgres_account_overage o
    WHERE o.enabled
    UNION ALL
    SELECT d.subject_id, 0::bigint, false
    FROM managed_postgres_overage_deliveries d
    WHERE d.period_start = sqlc.arg(period_start)::timestamptz
    UNION ALL
    SELECT p.subject_id, 0::bigint, false
    FROM managed_postgres_overage_pending p
    WHERE p.period_start = sqlc.arg(period_start)::timestamptz
) s
GROUP BY s.subject_id
ORDER BY s.subject_id;

-- name: ManagedPostgresOverageDelivered :one
SELECT d.live, d.billed_unit_seconds, d.delivered_millicents, d.updated_at, d.billed_storage_byte_seconds
FROM managed_postgres_overage_deliveries d
WHERE d.provider = sqlc.arg(provider)::text AND d.subject_id = sqlc.arg(subject_id)::uuid
  AND d.period_start = sqlc.arg(period_start)::timestamptz;

-- name: RecordManagedPostgresOverageDelivery :one
-- Advances the cumulative charge only from the row as read (previous_millicents
-- and previous_live; zero: no row yet), so a replica that lost the race records
-- nothing. A live delivery may replace a shadow row, which charged nothing; a
-- shadow delivery never replaces a live one (bug hunt 33).
INSERT INTO managed_postgres_overage_deliveries AS d (provider, subject_id, period_start, live,
    billed_unit_seconds, delivered_millicents, updated_at, billed_storage_byte_seconds)
VALUES (sqlc.arg(provider)::text, sqlc.arg(subject_id)::uuid, sqlc.arg(period_start)::timestamptz,
    sqlc.arg(live)::boolean, sqlc.arg(billed_unit_seconds)::bigint, sqlc.arg(delivered_millicents)::bigint,
    sqlc.arg(updated_at)::timestamptz, sqlc.arg(billed_storage_byte_seconds)::bigint)
ON CONFLICT (provider, subject_id, period_start) DO UPDATE
SET live = EXCLUDED.live, billed_unit_seconds = EXCLUDED.billed_unit_seconds,
    delivered_millicents = EXCLUDED.delivered_millicents, updated_at = EXCLUDED.updated_at,
    billed_storage_byte_seconds = EXCLUDED.billed_storage_byte_seconds
WHERE d.delivered_millicents = sqlc.arg(previous_millicents)::bigint
  AND d.live = sqlc.arg(previous_live)::boolean
  AND (EXCLUDED.live OR NOT d.live)
  AND (EXCLUDED.delivered_millicents > d.delivered_millicents OR (EXCLUDED.live AND NOT d.live))
RETURNING true AS recorded;

-- ADR-20261101143527: a step is reserved before apid sends it to the
-- provider, so a step that was sent but not recorded is sent again with the
-- same target instead of a new one charged on top of it.

-- name: ManagedPostgresOveragePending :one
SELECT p.live, p.step_at, p.used_unit_seconds, p.allowance_unit_seconds, p.billed_unit_seconds,
    p.from_millicents, p.target_millicents, p.used_storage_byte_seconds, p.storage_pool_bytes,
    p.billed_storage_byte_seconds, p.previous_millicents, p.previous_live
FROM managed_postgres_overage_pending p
WHERE p.provider = sqlc.arg(provider)::text AND p.subject_id = sqlc.arg(subject_id)::uuid
  AND p.period_start = sqlc.arg(period_start)::timestamptz;

-- name: ReserveManagedPostgresOverage :one
-- One reservation per provider, subject and month: a replica that finds one
-- reserves nothing.
INSERT INTO managed_postgres_overage_pending (provider, subject_id, period_start, live, step_at,
    used_unit_seconds, allowance_unit_seconds, billed_unit_seconds, from_millicents, target_millicents,
    used_storage_byte_seconds, storage_pool_bytes, billed_storage_byte_seconds, previous_millicents, previous_live)
VALUES (sqlc.arg(provider)::text, sqlc.arg(subject_id)::uuid, sqlc.arg(period_start)::timestamptz,
    sqlc.arg(live)::boolean, sqlc.arg(step_at)::timestamptz, sqlc.arg(used_unit_seconds)::bigint,
    sqlc.arg(allowance_unit_seconds)::bigint, sqlc.arg(billed_unit_seconds)::bigint,
    sqlc.arg(from_millicents)::bigint, sqlc.arg(target_millicents)::bigint,
    sqlc.arg(used_storage_byte_seconds)::bigint, sqlc.arg(storage_pool_bytes)::bigint,
    sqlc.arg(billed_storage_byte_seconds)::bigint, sqlc.arg(previous_millicents)::bigint,
    sqlc.arg(previous_live)::boolean)
ON CONFLICT (provider, subject_id, period_start) DO NOTHING
RETURNING true AS reserved;

-- name: ClearManagedPostgresOveragePending :exec
DELETE FROM managed_postgres_overage_pending
WHERE provider = sqlc.arg(provider)::text AND subject_id = sqlc.arg(subject_id)::uuid
  AND period_start = sqlc.arg(period_start)::timestamptz AND target_millicents = sqlc.arg(target_millicents)::bigint;

-- name: ListManagedPostgresOveragePending :many
-- Reservations survive month close and are paged without starving later keys
-- when an older provider delivery keeps failing.
SELECT subject_id,period_start FROM managed_postgres_overage_pending
WHERE provider=sqlc.arg(provider)::text AND period_start<=sqlc.arg(through)::timestamptz
 AND (sqlc.narg(after_period)::timestamptz IS NULL
  OR (period_start,subject_id)>(sqlc.narg(after_period)::timestamptz,sqlc.narg(after_subject)::uuid))
ORDER BY period_start,subject_id LIMIT sqlc.arg(page_limit)::int;
