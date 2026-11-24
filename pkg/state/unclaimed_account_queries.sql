-- Claimable deploys (ADR-20261124080000): unclaimed anonymous-deploy accounts.

-- name: LockUnclaimedAccountCreation :exec
-- Serialises anonymous account creation so the caps below are checked and
-- the row inserted as one step across every apid.
SELECT pg_advisory_xact_lock(hashtext('unclaimed_accounts.create'));

-- name: CountLiveUnclaimedAccounts :one
SELECT count(*)::int FROM unclaimed_accounts
 WHERE claimed_at IS NULL AND expired_at IS NULL AND expires_at > sqlc.arg(now)::timestamptz;

-- name: CountRecentUnclaimedAccountsFromNetwork :one
SELECT count(*) FILTER (WHERE created_ip = sqlc.arg(address)::inet)::int AS from_address,
       count(*)::int AS from_network
  FROM unclaimed_accounts
 WHERE created_at > sqlc.arg(since)::timestamptz
   AND created_ip <<= sqlc.arg(network)::cidr;

-- name: InsertUnclaimedAccount :exec
INSERT INTO unclaimed_accounts (account_id, claim_token_sha256, expires_at, created_ip, user_agent)
VALUES (sqlc.arg(account_id), sqlc.arg(claim_token_sha256), sqlc.arg(expires_at),
        sqlc.narg(created_ip)::inet, sqlc.arg(user_agent));

-- name: UnclaimedAccountByID :one
SELECT account_id::text AS account_id, claim_token_sha256, created_at, expires_at, claimed_at, expired_at,
       coalesce(host(created_ip), '')::text AS created_ip, user_agent
  FROM unclaimed_accounts
 WHERE account_id = sqlc.arg(account_id);

-- name: UnclaimedAccountByClaimToken :one
SELECT account_id::text AS account_id, claim_token_sha256, created_at, expires_at, claimed_at, expired_at,
       coalesce(host(created_ip), '')::text AS created_ip, user_agent
  FROM unclaimed_accounts
 WHERE claim_token_sha256 = sqlc.arg(claim_token_sha256);

-- name: RotateUnclaimedClaimToken :execrows
UPDATE unclaimed_accounts SET claim_token_sha256 = sqlc.arg(claim_token_sha256)
 WHERE account_id = sqlc.arg(account_id) AND claimed_at IS NULL AND expired_at IS NULL;

-- name: MarkUnclaimedAccountClaimed :execrows
UPDATE unclaimed_accounts SET claimed_at = sqlc.arg(at)
 WHERE account_id = sqlc.arg(account_id) AND claimed_at IS NULL AND expired_at IS NULL;

-- name: MarkUnclaimedAccountExpired :execrows
UPDATE unclaimed_accounts SET expired_at = sqlc.arg(at)
 WHERE account_id = sqlc.arg(account_id) AND claimed_at IS NULL AND expired_at IS NULL;

-- name: ListExpiredUnclaimedAccounts :many
-- Keyset pages by (expires_at, account_id), so rows the sweep cannot finish
-- never hide the ones after them.
SELECT account_id::text AS account_id, expires_at FROM unclaimed_accounts
 WHERE claimed_at IS NULL AND expired_at IS NULL AND expires_at <= sqlc.arg(now)::timestamptz
   AND (expires_at, account_id) > (sqlc.arg(after_expires_at)::timestamptz, sqlc.arg(after_account_id)::uuid)
 ORDER BY expires_at, account_id
 LIMIT sqlc.arg(row_limit);

-- name: LockLiveUnclaimedAccountByClaimToken :one
-- Holds the row while a claim confirmation is issued, so a concurrent link
-- rotation or expiry (both update this row) runs wholly before or after it.
SELECT account_id FROM unclaimed_accounts
 WHERE claim_token_sha256 = sqlc.arg(claim_token_sha256)
   AND claimed_at IS NULL AND expired_at IS NULL AND expires_at > sqlc.arg(now)::timestamptz
 FOR UPDATE;
