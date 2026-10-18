-- name: IssueEmailVerificationToken :exec
INSERT INTO email_verification_tokens(token_hash,account_id,expires_at) VALUES($1,$2,$3)
ON CONFLICT(token_hash) DO NOTHING;

-- Keep the account-before-token order used by producer handoff and erasure.
-- The materialized dependency acquires account custody before updating token.
-- Empty address is the unchanged legacy token format. New bound tokens never
-- verify a different current address, including after the maild handoff.
-- name: ConsumeEmailVerificationToken :one
WITH candidate AS MATERIALIZED (
    SELECT a.id FROM accounts a JOIN email_verification_tokens t ON t.account_id=a.id
    WHERE t.token_hash=$1 AND t.consumed_at IS NULL AND t.expires_at>clock_timestamp()
      AND (t.email_address='' OR (t.email_address=lower(a.email) AND a.status='active'))
    FOR UPDATE OF a
), consumed AS (
    UPDATE email_verification_tokens t SET consumed_at=clock_timestamp()
    FROM candidate c WHERE t.token_hash=$1 AND t.account_id=c.id AND t.consumed_at IS NULL AND t.expires_at>clock_timestamp()
    RETURNING t.account_id
)
UPDATE accounts a SET email_verified_at=coalesce(a.email_verified_at,clock_timestamp())
FROM consumed c WHERE a.id=c.account_id RETURNING a.id;
