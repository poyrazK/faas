-- Enrollment and policy changes invalidate saved disable-email links, including
-- while issuance/delivery gates are off. Burns of recovery codes keep the factor.
-- Durable links follow mfa_revision; the legacy inline requests carry no
-- revision, so each statement consumes them too (bug hunt 2026-10-28 H98).
-- name: SetMFASecret :execrows
WITH legacy AS (
    UPDATE mfa_disable_requests SET consumed_at=clock_timestamp() WHERE account_id=$1 AND consumed_at IS NULL
)
UPDATE accounts SET mfa_secret_encrypted=$2,mfa_recovery_codes_hash=$3,mfa_revision=mfa_revision+1 WHERE id=$1;

-- name: MarkMFAEnrolled :execrows
WITH legacy AS (
    UPDATE mfa_disable_requests SET consumed_at=clock_timestamp() WHERE account_id=$1 AND consumed_at IS NULL
)
UPDATE accounts SET mfa_enrolled_at=clock_timestamp(),mfa_required=false,mfa_revision=mfa_revision+1 WHERE id=$1;

-- A setup request cannot replace a factor confirmed while it was in flight.
-- name: SavePendingMFASecret :one
WITH changed AS (
    UPDATE accounts SET mfa_secret_encrypted=$2,mfa_recovery_codes_hash=$3,
        mfa_totp_last_step=NULL,mfa_revision=mfa_revision+1
    WHERE id=$1 AND mfa_enrolled_at IS NULL RETURNING id
), legacy AS (
    UPDATE mfa_disable_requests SET consumed_at=clock_timestamp()
    WHERE account_id IN (SELECT id FROM changed) AND consumed_at IS NULL
)
SELECT id FROM changed;

-- Enrollment and spending its proof commit together against the exact factor
-- verified by the caller. A stale or repeated confirmation changes nothing.
-- name: ConfirmPendingMFA :one
WITH changed AS (
    UPDATE accounts SET mfa_enrolled_at=clock_timestamp(),mfa_required=false,
        mfa_totp_last_step=sqlc.arg(step)::bigint,mfa_revision=mfa_revision+1
    WHERE id=$1 AND mfa_enrolled_at IS NULL AND mfa_secret_encrypted=$2
      AND (mfa_totp_last_step IS NULL OR mfa_totp_last_step<sqlc.arg(step)::bigint)
    RETURNING id
), legacy AS (
    UPDATE mfa_disable_requests SET consumed_at=clock_timestamp()
    WHERE account_id IN (SELECT id FROM changed) AND consumed_at IS NULL
)
SELECT id FROM changed;

-- name: ClearMFA :execrows
WITH legacy AS (
    UPDATE mfa_disable_requests SET consumed_at=clock_timestamp() WHERE account_id=$1 AND consumed_at IS NULL
)
UPDATE accounts SET mfa_secret_encrypted=NULL,mfa_recovery_codes_hash=NULL,mfa_enrolled_at=NULL,mfa_revision=mfa_revision+1 WHERE id=$1;

-- name: SetMFARequired :execrows
WITH legacy AS (
    UPDATE mfa_disable_requests SET consumed_at=clock_timestamp() WHERE account_id=$1 AND consumed_at IS NULL
)
UPDATE accounts SET mfa_required=$2,mfa_revision=mfa_revision+1 WHERE id=$1 AND mfa_required<>$2;

-- Keep legacy reissuance mutually exclusive with saved durable credentials.
-- Lock account first, then tokens, matching durable issuance and confirmation.
-- name: IssueMFADisableRequest :execrows
WITH account AS MATERIALIZED (
    SELECT id FROM accounts WHERE id=$2 FOR UPDATE
), invalidated AS (
    UPDATE mfa_disable_requests r SET consumed_at=clock_timestamp()
    FROM account a WHERE r.account_id=a.id AND r.consumed_at IS NULL
), durable AS (
    UPDATE mfa_disable_tokens t SET consumed_at=clock_timestamp()
    FROM account a WHERE t.account_id=a.id AND t.consumed_at IS NULL
)
INSERT INTO mfa_disable_requests(token_hash,account_id,requested_at)
SELECT $1,id,$3 FROM account;

-- A successful second-factor sign-in, or the owner pressing Cancel, ends every
-- pending email-assisted disable (F14-1, ADR-20261115100000). Account lock
-- first, then requests and tokens, matching issuance.
-- name: CancelPendingMFADisable :one
WITH account AS MATERIALIZED (
    SELECT id FROM accounts WHERE id=$1 FOR UPDATE
), legacy AS (
    UPDATE mfa_disable_requests r SET consumed_at=clock_timestamp()
    FROM account a WHERE r.account_id=a.id AND r.consumed_at IS NULL
    RETURNING 1
), durable AS (
    UPDATE mfa_disable_tokens t SET consumed_at=clock_timestamp()
    FROM account a WHERE t.account_id=a.id AND t.consumed_at IS NULL AND t.expires_at>clock_timestamp()
    RETURNING 1
)
SELECT ((SELECT count(*) FROM legacy) + (SELECT count(*) FROM durable))::bigint AS cancelled;

-- The earliest still-usable pending email-assisted disable, for the account
-- page's banner. Legacy requests carry no expiry column; they stop working
-- window_seconds after they were requested.
-- name: PendingMFADisableRequestedAt :one
SELECT min(pending.requested_at)::timestamptz AS requested_at FROM (
    SELECT r.requested_at FROM mfa_disable_requests r
    WHERE r.account_id=sqlc.arg(account_id)::uuid AND r.consumed_at IS NULL
      AND r.requested_at > clock_timestamp() - make_interval(secs => sqlc.arg(window_seconds)::float8)
    UNION ALL
    SELECT t.requested_at FROM mfa_disable_tokens t
    WHERE t.account_id=sqlc.arg(account_id)::uuid AND t.consumed_at IS NULL AND t.expires_at>clock_timestamp()
) pending;
