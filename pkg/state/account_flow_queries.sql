-- Account flow queries (ADR 20261107103000).

-- name: ClearPendingAccountEmailChanges :exec
DELETE FROM account_email_changes WHERE account_id=$1 AND consumed_at IS NULL;

-- name: InsertAccountEmailChange :execrows
INSERT INTO account_email_changes(token_hash,account_id,old_email,new_email,expires_at)
SELECT sqlc.arg(token_hash), a.id, lower(a.email::text), sqlc.arg(new_email)::text, sqlc.arg(expires_at)
FROM accounts a WHERE a.id=sqlc.arg(account_id) AND a.status='active';

-- Lock the account before the change row, like verification and reset. The
-- change applies only while the account still has the address it was
-- requested from. A unique violation on accounts.email rolls the whole
-- statement back and leaves the link usable. Password-reset links and legacy
-- MFA-disable requests are bound to the account, not the address they were
-- mailed to, so the change spends every outstanding one: the old mailbox
-- must not be able to take the account back.
-- name: ConsumeAccountEmailChange :one
WITH candidate AS MATERIALIZED (
    SELECT a.id FROM accounts a JOIN account_email_changes c ON c.account_id=a.id
    WHERE c.token_hash=$1 AND c.consumed_at IS NULL AND c.expires_at>clock_timestamp()
      AND lower(a.email::text)=c.old_email AND a.status='active'
    FOR UPDATE OF a
), consumed AS (
    UPDATE account_email_changes c SET consumed_at=clock_timestamp()
    FROM candidate x WHERE c.token_hash=$1 AND c.account_id=x.id AND c.consumed_at IS NULL
    RETURNING c.account_id, c.old_email, c.new_email
), spent_resets AS (
    UPDATE login_tokens t SET consumed_at=clock_timestamp()
    FROM consumed c WHERE t.account_id=c.account_id AND t.consumed_at IS NULL
    RETURNING t.account_id
), spent_mfa_disables AS (
    UPDATE mfa_disable_requests m SET consumed_at=clock_timestamp()
    FROM consumed c WHERE m.account_id=c.account_id AND m.consumed_at IS NULL
    RETURNING m.account_id
)
UPDATE accounts a SET email=c.new_email, email_verified_at=clock_timestamp()
FROM consumed c WHERE a.id=c.account_id
RETURNING a.id, c.old_email, c.new_email;

-- name: ReplaceAccountRecoveryCodes :execrows
UPDATE accounts SET mfa_recovery_codes_hash=sqlc.arg(hashes)::bytea[]
WHERE id=sqlc.arg(account_id) AND mfa_enrolled_at IS NOT NULL;

-- name: ListAccountOAuthLinks :many
SELECT provider, provider_subject, email, email_verified, created_at
FROM oauth_links WHERE account_id=$1 ORDER BY created_at, provider;

-- Removing a provider also records it, so signing in with it does not
-- re-link it by matching email (ADR 20261107103000).
-- name: DeleteAccountOAuthLink :one
WITH removed AS (
    DELETE FROM oauth_links l WHERE l.account_id=sqlc.arg(account_id) AND l.provider=sqlc.arg(provider)
    RETURNING l.account_id AS removed_account, l.provider AS removed_provider
), marked AS (
    INSERT INTO account_oauth_unlinks AS u (account_id, provider)
    SELECT DISTINCT r.removed_account, r.removed_provider FROM removed r
    ON CONFLICT (account_id, provider) DO UPDATE SET unlinked_at=clock_timestamp()
    RETURNING u.account_id
)
SELECT count(*)::bigint AS removed FROM removed;

-- name: AccountOAuthProviderUnlinked :one
SELECT EXISTS(SELECT 1 FROM account_oauth_unlinks WHERE account_id=$1 AND provider=$2)::boolean AS unlinked;

-- name: ClearAccountOAuthUnlink :exec
DELETE FROM account_oauth_unlinks WHERE account_id=$1 AND provider=$2;

-- name: AccountDisplayName :one
SELECT display_name FROM accounts WHERE id=$1;

-- name: AccountDisplayNames :many
SELECT id, display_name FROM accounts
WHERE id = ANY(sqlc.arg(ids)::uuid[]) AND display_name <> '';

-- name: SetAccountDisplayName :execrows
UPDATE accounts SET display_name=sqlc.arg(display_name)::text WHERE id=sqlc.arg(account_id);

-- name: RecentAccountSessions :many
SELECT coalesce(host(issued_ip),'')::text AS issued_ip, coalesce(issued_ua,'')::text AS issued_ua, issued_at
FROM sessions WHERE account_id=$1 AND issued_at>=sqlc.arg(since)
ORDER BY issued_at DESC LIMIT 200;

-- Remembered browsers for two-factor authentication (ADR 20261108150000).
-- name: InsertMFATrustedBrowser :execrows
INSERT INTO mfa_trusted_browsers(token_hash, account_id, ua_family, created_ip, expires_at)
SELECT sqlc.arg(token_hash), a.id, sqlc.arg(ua_family)::text, sqlc.arg(created_ip)::text, sqlc.arg(expires_at)
FROM accounts a WHERE a.id=sqlc.arg(account_id) AND a.mfa_enrolled_at IS NOT NULL;

-- Keep only the newest max_active remembered browsers per account.
-- name: TrimMFATrustedBrowsers :execrows
UPDATE mfa_trusted_browsers SET revoked_at=clock_timestamp()
WHERE id IN (
    SELECT t.id FROM mfa_trusted_browsers t
    WHERE t.account_id=sqlc.arg(account_id) AND t.revoked_at IS NULL
    ORDER BY t.created_at DESC OFFSET sqlc.arg(max_active)::int
);

-- A browser remembered before the current enrollment (MFA turned off and
-- set up again) is not trusted.
-- name: UseMFATrustedBrowser :execrows
UPDATE mfa_trusted_browsers t SET last_used_at=clock_timestamp()
FROM accounts a
WHERE t.token_hash=sqlc.arg(token_hash) AND t.account_id=sqlc.arg(account_id)
  AND t.ua_family=sqlc.arg(ua_family)::text AND t.revoked_at IS NULL AND t.expires_at>clock_timestamp()
  AND a.id=t.account_id AND a.mfa_enrolled_at IS NOT NULL AND t.created_at>=a.mfa_enrolled_at;

-- name: ListMFATrustedBrowsers :many
SELECT id, ua_family, created_ip, created_at, expires_at, last_used_at
FROM mfa_trusted_browsers
WHERE account_id=$1 AND revoked_at IS NULL AND expires_at>clock_timestamp()
ORDER BY created_at DESC;

-- name: RevokeMFATrustedBrowser :execrows
UPDATE mfa_trusted_browsers SET revoked_at=clock_timestamp()
WHERE id=sqlc.arg(id) AND account_id=sqlc.arg(account_id) AND revoked_at IS NULL;

-- name: RevokeAllMFATrustedBrowsers :execrows
UPDATE mfa_trusted_browsers SET revoked_at=clock_timestamp()
WHERE account_id=$1 AND revoked_at IS NULL;

-- Count one mail of kind to the account when its fixed window still has
-- room and the last send is at least min_interval old; no row means the
-- send is throttled (ADR 20261108160000).
-- name: TakeAccountMailSlot :one
INSERT INTO account_mail_throttle AS t (account_id, kind, window_started_at, sent_in_window, last_sent_at)
VALUES (sqlc.arg(account_id), sqlc.arg(kind)::text, clock_timestamp(), 1, clock_timestamp())
ON CONFLICT (account_id, kind) DO UPDATE SET
    window_started_at = CASE WHEN t.window_started_at <= clock_timestamp() - make_interval(secs => sqlc.arg(window_seconds)::double precision)
        THEN clock_timestamp() ELSE t.window_started_at END,
    sent_in_window = CASE WHEN t.window_started_at <= clock_timestamp() - make_interval(secs => sqlc.arg(window_seconds)::double precision)
        THEN 1 ELSE t.sent_in_window + 1 END,
    last_sent_at = clock_timestamp()
WHERE t.last_sent_at <= clock_timestamp() - make_interval(secs => sqlc.arg(min_interval_seconds)::double precision)
  AND (t.window_started_at <= clock_timestamp() - make_interval(secs => sqlc.arg(window_seconds)::double precision)
       OR t.sent_in_window < sqlc.arg(max_per_window)::int)
RETURNING t.sent_in_window;

-- Grants an account started that a recovery must end (ADR 20261109120000):
-- pending email changes, org invitations it created, and CLI or agent
-- sign-in codes approved but not yet exchanged for a key.
-- name: EndAccountPendingEmailChanges :execrows
DELETE FROM account_email_changes WHERE account_id=$1 AND consumed_at IS NULL;

-- name: EndAccountPendingInvitations :execrows
UPDATE org_invitations SET revoked_at=clock_timestamp()
WHERE invited_by_account_id=$1 AND consumed_at IS NULL AND revoked_at IS NULL;

-- name: EndAccountApprovedCliCodes :execrows
UPDATE cli_auth_codes SET status='expired'
WHERE account_id=$1 AND status='consumed' AND consumed_at IS NULL;
