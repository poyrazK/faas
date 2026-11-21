-- ADR-20261025094915: compute cap thresholds a usage subject reached in a
-- UTC month. One row per subject, month and threshold; the first sweep to
-- see a threshold records it and later sweeps leave it alone.

-- name: RecordManagedPostgresCapEvent :one
INSERT INTO managed_postgres_cap_events (subject_id, period_start, threshold, reached_at,
    used_unit_seconds, allowance_unit_seconds, ceiling_unit_seconds, overage_enabled)
VALUES (sqlc.arg(subject_id)::uuid, sqlc.arg(period_start)::timestamptz, sqlc.arg(threshold)::text,
    sqlc.arg(reached_at)::timestamptz, sqlc.arg(used_unit_seconds)::bigint, sqlc.arg(allowance_unit_seconds)::bigint,
    sqlc.arg(ceiling_unit_seconds)::bigint, sqlc.arg(overage_enabled)::boolean)
ON CONFLICT (subject_id, period_start, threshold) DO NOTHING
RETURNING true AS recorded;

-- name: ManagedPostgresCapSuspendedAt :one
SELECT e.reached_at FROM managed_postgres_cap_events e
WHERE e.subject_id = sqlc.arg(subject_id)::uuid AND e.period_start = sqlc.arg(period_start)::timestamptz
  AND e.threshold = 'suspended';

-- name: ManagedPostgresMostSevereCapEvent :one
SELECT e.threshold, e.reached_at, e.used_unit_seconds, e.allowance_unit_seconds, e.ceiling_unit_seconds,
    e.overage_enabled, (e.mail_intent_id IS NOT NULL)::boolean AS mailed
FROM managed_postgres_cap_events e
WHERE e.subject_id = sqlc.arg(subject_id)::uuid AND e.period_start = sqlc.arg(period_start)::timestamptz
  AND e.threshold <> 'egress_held'
ORDER BY array_position(ARRAY['warning', 'allowance', 'ceiling_warning', 'suspended']::text[], e.threshold) DESC
LIMIT 1;

-- name: ManagedPostgresCapEvent :one
-- ADR-20261110160000: one threshold's event of the month; the egress hold is
-- mailed through it, apart from the compute thresholds ranked above.
SELECT e.reached_at, e.used_unit_seconds, e.allowance_unit_seconds, e.ceiling_unit_seconds,
    e.overage_enabled, (e.mail_intent_id IS NOT NULL)::boolean AS mailed
FROM managed_postgres_cap_events e
WHERE e.subject_id = sqlc.arg(subject_id)::uuid AND e.period_start = sqlc.arg(period_start)::timestamptz
  AND e.threshold = sqlc.arg(threshold)::text;

-- name: RecordManagedPostgresEgressHoldNotice :one
-- ADR-20261110160000 amendment 2026-11-13: the first sweep to hold a database
-- for its own egress allowance in a month records it; later sweeps leave it.
INSERT INTO managed_postgres_egress_hold_notices (database_id, period_start, subject_id, reached_at,
    used_bytes, allowance_bytes, ceiling_bytes, overage_billed)
VALUES (sqlc.arg(database_id)::uuid, sqlc.arg(period_start)::timestamptz, sqlc.arg(subject_id)::uuid,
    sqlc.arg(reached_at)::timestamptz, sqlc.arg(used_bytes)::bigint, sqlc.arg(allowance_bytes)::bigint,
    sqlc.arg(ceiling_bytes)::bigint, sqlc.arg(overage_billed)::boolean)
ON CONFLICT (database_id, period_start) DO NOTHING
RETURNING true AS recorded;

-- name: ManagedPostgresEgressHoldNotice :one
SELECT n.subject_id, n.reached_at, n.used_bytes, n.allowance_bytes, n.ceiling_bytes, n.overage_billed,
    (n.mail_intent_id IS NOT NULL)::boolean AS mailed
FROM managed_postgres_egress_hold_notices n
WHERE n.database_id = sqlc.arg(database_id)::uuid AND n.period_start = sqlc.arg(period_start)::timestamptz;
