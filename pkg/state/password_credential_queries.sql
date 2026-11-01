-- Password writers lock/update the account before its credential row, matching
-- reset redemption, producer handoff and account erasure. The revision survives
-- password deletion, so restoring an old PHC never revives an older reset link.
-- A password change also ends the account's legacy MFA-disable email
-- requests, which carry no revision (bug hunt 2026-10-28 H98).
-- name: SetAccountPassword :execrows
WITH changed AS (
    UPDATE accounts SET password_reset_revision=password_reset_revision+1
    WHERE id=$1 RETURNING id
), legacy AS (
    UPDATE mfa_disable_requests SET consumed_at=clock_timestamp() WHERE account_id=$1 AND consumed_at IS NULL
)
INSERT INTO account_passwords(account_id,hash,updated_at)
SELECT id,$2,clock_timestamp() FROM changed
ON CONFLICT(account_id) DO UPDATE SET hash=excluded.hash,updated_at=excluded.updated_at;

-- name: DeleteAccountPassword :exec
WITH changed AS (
    UPDATE accounts SET password_reset_revision=password_reset_revision+1
    WHERE id=$1 RETURNING id
)
DELETE FROM account_passwords p USING changed a WHERE p.account_id=a.id;
