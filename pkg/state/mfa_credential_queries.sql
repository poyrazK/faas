-- Enrollment and policy changes invalidate saved disable-email links, including
-- while issuance/delivery gates are off. Burns of recovery codes keep the factor.
-- name: SetMFASecret :execrows
UPDATE accounts SET mfa_secret_encrypted=$2,mfa_recovery_codes_hash=$3,mfa_revision=mfa_revision+1 WHERE id=$1;

-- name: MarkMFAEnrolled :execrows
UPDATE accounts SET mfa_enrolled_at=clock_timestamp(),mfa_required=false,mfa_revision=mfa_revision+1 WHERE id=$1;

-- name: ClearMFA :execrows
UPDATE accounts SET mfa_secret_encrypted=NULL,mfa_recovery_codes_hash=NULL,mfa_enrolled_at=NULL,mfa_revision=mfa_revision+1 WHERE id=$1;

-- name: SetMFARequired :execrows
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
