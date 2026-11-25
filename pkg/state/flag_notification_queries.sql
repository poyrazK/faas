-- Flag notifications (ADR-20261125085913).

-- name: GetFlagNotificationSettings :one
SELECT * FROM flag_notification_settings
WHERE environment_id = sqlc.arg(environment_id)::uuid;

-- name: UpsertFlagNotificationSettings :one
INSERT INTO flag_notification_settings (environment_id, account_id, channel_ids, updated_by)
VALUES (sqlc.arg(environment_id)::uuid, sqlc.arg(account_id)::uuid, sqlc.arg(channel_ids)::uuid[], sqlc.arg(updated_by)::text)
ON CONFLICT (environment_id) DO UPDATE
SET channel_ids = EXCLUDED.channel_ids, updated_by = EXCLUDED.updated_by, updated_at = now()
RETURNING *;

-- name: EnqueueFlagNotification :execrows
-- Queues a notification unless its key was ever queued: the key outlives
-- the outbox row, so a swept notification is not sent again.
WITH k AS (
    INSERT INTO flag_notification_keys (key, environment_id, kind)
    VALUES (sqlc.arg(key)::text, sqlc.arg(environment_id)::uuid, sqlc.arg(kind)::text)
    ON CONFLICT (key) DO NOTHING
    RETURNING key
)
INSERT INTO flag_notifications (account_id, org_id, environment_id, kind, key, subject, summary, channel_ids, next_attempt_at)
SELECT sqlc.arg(account_id)::uuid, sqlc.narg(org_id)::uuid, sqlc.arg(environment_id)::uuid, sqlc.arg(kind)::text, k.key,
    sqlc.arg(subject)::text, sqlc.arg(summary)::text, sqlc.arg(channel_ids)::uuid[], sqlc.arg(now)::timestamptz
FROM k
ON CONFLICT (key) DO NOTHING;

-- name: ClaimDueFlagNotifications :many
-- Leases pending notifications whose next attempt is due, oldest first,
-- counting the attempt, with the account's plan.
WITH due AS (
    SELECT n.id FROM flag_notifications n
    WHERE n.status = 'pending' AND n.next_attempt_at <= sqlc.arg(now)::timestamptz
    ORDER BY n.next_attempt_at, n.id
    LIMIT sqlc.arg(max_rows)::int
    FOR UPDATE SKIP LOCKED
)
UPDATE flag_notifications n SET next_attempt_at = sqlc.arg(lease_until)::timestamptz, attempts = n.attempts + 1
FROM due, accounts a
WHERE n.id = due.id AND a.id = n.account_id
RETURNING n.*, a.plan::text AS plan;

-- name: SettleFlagNotification :execrows
-- Records an attempt: delivered, abandoned, or retried at next_attempt_at
-- to the channels still owed it. Only the claim that counted
-- claimed_attempts settles: a row reclaimed after its lease ran out is not
-- settled by the older claim.
UPDATE flag_notifications
SET status = sqlc.arg(status)::text, last_error = sqlc.arg(last_error)::text,
    next_attempt_at = sqlc.arg(next_attempt_at)::timestamptz,
    attempts = sqlc.arg(attempts)::int, channel_ids = sqlc.arg(channel_ids)::uuid[],
    settled_at = CASE WHEN sqlc.arg(status)::text = 'pending' THEN NULL ELSE sqlc.arg(now)::timestamptz END
WHERE id = sqlc.arg(id)::uuid AND status = 'pending' AND attempts = sqlc.arg(claimed_attempts)::int;

-- name: SweepFlagNotifications :execrows
-- Deletes settled notifications settled before cutoff, at most max_rows.
DELETE FROM flag_notifications
WHERE id IN (
    SELECT id FROM flag_notifications
    WHERE status <> 'pending' AND settled_at < sqlc.arg(cutoff)::timestamptz
    ORDER BY settled_at
    LIMIT sqlc.arg(max_rows)::int);

-- name: SweepFlagNotificationDigestKeys :execrows
-- Deletes weekly digest keys queued before cutoff, at most max_rows: a past
-- week's key is never queued again.
DELETE FROM flag_notification_keys
WHERE key IN (
    SELECT key FROM flag_notification_keys
    WHERE kind = 'lifecycle_digest' AND created_at < sqlc.arg(cutoff)::timestamptz
    ORDER BY created_at
    LIMIT sqlc.arg(max_rows)::int);

-- name: FlagNotificationChannels :many
-- The account's notification channels among ids; a deleted channel is
-- skipped.
SELECT c.* FROM notification_channels c
WHERE c.account_id = sqlc.arg(account_id)::uuid AND c.id = ANY(sqlc.arg(channel_ids)::uuid[])
ORDER BY c.id;
