-- Saved insights (ADR-20261122190000).

-- name: CreateSavedInsight :one
-- Creates a saved insight while the environment holds fewer than max_insights.
INSERT INTO saved_insights (account_id, environment_id, key, name, kind, query, range_days, digest,
                            alert_comparison, alert_threshold, channel_ids, next_digest_at, next_check_at, created_by)
SELECT sqlc.arg(account_id)::uuid, sqlc.arg(environment_id)::uuid, sqlc.arg(key)::text, sqlc.arg(name)::text,
       sqlc.arg(kind)::text, sqlc.arg(query)::jsonb, sqlc.arg(range_days)::int, sqlc.arg(digest)::text,
       sqlc.arg(alert_comparison)::text, sqlc.arg(alert_threshold)::float8, sqlc.arg(channel_ids)::uuid[],
       sqlc.narg(next_digest_at)::timestamptz, sqlc.narg(next_check_at)::timestamptz, sqlc.arg(created_by)::text
WHERE (SELECT count(*) FROM saved_insights s WHERE s.environment_id = sqlc.arg(environment_id)::uuid) < sqlc.arg(max_insights)::bigint
RETURNING *;

-- name: UpdateSavedInsight :one
-- Replaces a saved insight's definition and schedule. A changed alert
-- starts from ok again.
UPDATE saved_insights
SET name = sqlc.arg(name)::text, kind = sqlc.arg(kind)::text, query = sqlc.arg(query)::jsonb,
    range_days = sqlc.arg(range_days)::int, digest = sqlc.arg(digest)::text,
    alert_comparison = sqlc.arg(alert_comparison)::text, alert_threshold = sqlc.arg(alert_threshold)::float8,
    channel_ids = sqlc.arg(channel_ids)::uuid[], next_digest_at = sqlc.narg(next_digest_at)::timestamptz,
    next_check_at = sqlc.narg(next_check_at)::timestamptz,
    alert_state = CASE WHEN alert_comparison = sqlc.arg(alert_comparison)::text
                        AND alert_threshold = sqlc.arg(alert_threshold)::float8
                        AND query = sqlc.arg(query)::jsonb AND range_days = sqlc.arg(range_days)::int
                       THEN alert_state ELSE 'ok' END,
    updated_at = now()
WHERE environment_id = sqlc.arg(environment_id)::uuid AND key = sqlc.arg(key)::text
RETURNING *;

-- name: GetSavedInsight :one
SELECT * FROM saved_insights
WHERE environment_id = sqlc.arg(environment_id)::uuid AND key = sqlc.arg(key)::text;

-- name: ListSavedInsights :many
SELECT * FROM saved_insights
WHERE environment_id = sqlc.arg(environment_id)::uuid
ORDER BY key;

-- name: DeleteSavedInsight :execrows
DELETE FROM saved_insights
WHERE environment_id = sqlc.arg(environment_id)::uuid AND key = sqlc.arg(key)::text;

-- name: ClaimDueSavedInsights :many
-- Leases saved insights whose digest or alert check is due, oldest first,
-- with the account's plan and the environment's project.
WITH due AS (
    SELECT s.id FROM saved_insights s
    WHERE s.next_check_at <= sqlc.arg(now)::timestamptz
      AND (s.lease_until IS NULL OR s.lease_until < sqlc.arg(now)::timestamptz)
    ORDER BY s.next_check_at, s.id
    LIMIT sqlc.arg(max_rows)::int
    FOR UPDATE SKIP LOCKED
)
UPDATE saved_insights s SET lease_until = sqlc.arg(lease_until)::timestamptz
FROM due, accounts a, project_environments e
WHERE s.id = due.id AND a.id = s.account_id AND e.id = s.environment_id
RETURNING s.*, a.plan::text AS plan, e.project_id AS project_id, e.slug AS environment_slug;

-- name: SettleSavedInsight :execrows
-- Records a run's outcome and releases the lease. A definition changed
-- since the claim (updated_at) keeps its new schedule.
UPDATE saved_insights
SET last_value = sqlc.narg(last_value)::float8, last_run_at = sqlc.arg(now)::timestamptz,
    last_error = sqlc.arg(last_error)::text, alert_state = sqlc.arg(alert_state)::text,
    next_digest_at = sqlc.narg(next_digest_at)::timestamptz, next_check_at = sqlc.narg(next_check_at)::timestamptz,
    lease_until = NULL
WHERE id = sqlc.arg(id)::uuid AND updated_at = sqlc.arg(claimed_updated_at)::timestamptz;

-- name: LockSavedInsightEnvironment :exec
-- Serializes creates in one environment so the cap holds under concurrency.
SELECT 1 FROM project_environments WHERE id = sqlc.arg(environment_id)::uuid FOR UPDATE;

-- name: ReleaseSavedInsightLease :exec
UPDATE saved_insights SET lease_until = NULL WHERE id = sqlc.arg(id)::uuid;

-- name: SavedInsightChannels :many
-- The account's notification channels a saved insight sends to; a deleted
-- channel is skipped.
SELECT c.* FROM notification_channels c
WHERE c.account_id = sqlc.arg(account_id)::uuid AND c.id = ANY(sqlc.arg(channel_ids)::uuid[])
ORDER BY c.id;
