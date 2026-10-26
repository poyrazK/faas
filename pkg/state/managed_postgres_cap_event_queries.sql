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
ORDER BY array_position(ARRAY['warning', 'allowance', 'ceiling_warning', 'suspended']::text[], e.threshold) DESC
LIMIT 1;
