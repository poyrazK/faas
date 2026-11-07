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
-- statement back and leaves the link usable.
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

-- name: DeleteAccountOAuthLink :execrows
DELETE FROM oauth_links WHERE account_id=$1 AND provider=$2;

-- name: AccountDisplayName :one
SELECT display_name FROM accounts WHERE id=$1;

-- name: SetAccountDisplayName :execrows
UPDATE accounts SET display_name=sqlc.arg(display_name)::text WHERE id=sqlc.arg(account_id);

-- name: RecentAccountSessions :many
SELECT host(issued_ip)::text AS issued_ip, coalesce(issued_ua,'')::text AS issued_ua, issued_at
FROM sessions WHERE account_id=$1 AND issued_at>=sqlc.arg(since)
ORDER BY issued_at DESC LIMIT 200;
