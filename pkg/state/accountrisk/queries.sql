-- name: ListStrikes :many
SELECT reason, app_id, struck_at
FROM account_abuse_strikes
WHERE account_id = sqlc.arg(account_id)::uuid AND struck_at > sqlc.arg(since)::timestamptz
ORDER BY struck_at DESC
LIMIT 100;

-- name: HeldLinksTo :many
-- Accounts flagged as resembling this (held) account.
SELECT account_id, held_org_id, held_reason, signal, detected_at
FROM account_held_links
WHERE held_account_id = sqlc.arg(held_account_id)::uuid
ORDER BY detected_at DESC, account_id, signal
LIMIT 200;

-- name: RecentHeldLinks :many
-- Links detected since a time, newest first, with the new account's
-- sign-up country and email domain for a review list.
SELECT l.account_id, l.held_account_id, l.held_org_id, l.held_reason, l.signal, l.detected_at,
       coalesce(f.country, '')::text AS country, coalesce(f.email_domain, '')::text AS email_domain,
       coalesce(f.disposable_email, false)::boolean AS disposable_email
FROM account_held_links l
LEFT JOIN account_signup_facts f ON f.account_id = l.account_id
WHERE l.detected_at >= sqlc.arg(since)::timestamptz
ORDER BY l.detected_at DESC, l.account_id, l.held_account_id, l.signal
LIMIT sqlc.arg(max_rows)::int;

-- name: AbuseEvents :many
-- Abuse events about an account: the hold (subject: the account), CPU
-- signals (the app), abuse scans (the deployment) and abuse recycles (the
-- instance), newest first.
SELECT e.at, e.actor, e.kind, e.subject, coalesce(e.data, '{}'::jsonb)::jsonb AS data
FROM events e
WHERE e.at >= sqlc.arg(since)::timestamptz
  AND e.kind = ANY(sqlc.arg(kinds)::text[])
  AND e.subject IN (
    SELECT sqlc.arg(account_id)::uuid
    UNION ALL SELECT a.id FROM apps a WHERE a.account_id = sqlc.arg(account_id)::uuid
    UNION ALL SELECT d.id FROM deployments d JOIN apps a ON a.id = d.app_id WHERE a.account_id = sqlc.arg(account_id)::uuid
    UNION ALL SELECT i.id FROM instances i JOIN apps a ON a.id = i.app_id WHERE a.account_id = sqlc.arg(account_id)::uuid
  )
ORDER BY e.at DESC, e.id DESC
LIMIT sqlc.arg(max_rows)::int;
