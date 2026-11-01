-- ADR-20261031110037: managed PostgreSQL overage charges delivered per
-- billing provider, usage subject and UTC month.

-- name: ManagedPostgresOverageSubjects :many
SELECT e.subject_id, e.allowance_unit_seconds, e.overage_enabled
FROM managed_postgres_cap_events e
WHERE e.period_start = sqlc.arg(period_start)::timestamptz AND e.threshold = 'allowance'
ORDER BY e.subject_id;

-- name: ManagedPostgresOverageDelivered :one
SELECT d.live, d.billed_unit_seconds, d.delivered_millicents, d.updated_at
FROM managed_postgres_overage_deliveries d
WHERE d.provider = sqlc.arg(provider)::text AND d.subject_id = sqlc.arg(subject_id)::uuid
  AND d.period_start = sqlc.arg(period_start)::timestamptz;

-- name: RecordManagedPostgresOverageDelivery :one
-- Advances the cumulative charge only from previous_millicents (zero: no row
-- yet), so a replica that lost the race records nothing.
INSERT INTO managed_postgres_overage_deliveries AS d (provider, subject_id, period_start, live,
    billed_unit_seconds, delivered_millicents, updated_at)
VALUES (sqlc.arg(provider)::text, sqlc.arg(subject_id)::uuid, sqlc.arg(period_start)::timestamptz,
    sqlc.arg(live)::boolean, sqlc.arg(billed_unit_seconds)::bigint, sqlc.arg(delivered_millicents)::bigint,
    sqlc.arg(updated_at)::timestamptz)
ON CONFLICT (provider, subject_id, period_start) DO UPDATE
SET live = EXCLUDED.live, billed_unit_seconds = EXCLUDED.billed_unit_seconds,
    delivered_millicents = EXCLUDED.delivered_millicents, updated_at = EXCLUDED.updated_at
WHERE d.delivered_millicents = sqlc.arg(previous_millicents)::bigint
  AND EXCLUDED.delivered_millicents > d.delivered_millicents
RETURNING true AS recorded;
