-- ADR-20261031110037: managed PostgreSQL overage charges delivered per
-- billing provider, usage subject and UTC month.

-- name: ManagedPostgresOverageSubjects :many
-- Subjects that may owe overage for the month: those that reached the
-- compute allowance (as the cap sweep recorded it), those with overage on,
-- whose storage past the pool is billed (ADR-20261031110037 amendment 2), and
-- those already charged.
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
