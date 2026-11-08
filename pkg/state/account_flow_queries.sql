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
