-- filename: 20261017091735652_platform_mail_mfa_disable_outbox.sql

-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
-- MFA enrollment/secret/policy mutations advance this counter even with mail
-- flags off. Recovery-code burns do not change the enrolled factor.
ALTER TABLE accounts ADD COLUMN IF NOT EXISTS mfa_revision bigint NOT NULL DEFAULT 1 CHECK (mfa_revision>0);
CREATE TABLE IF NOT EXISTS mfa_disable_tokens (
    token_hash bytea PRIMARY KEY CHECK (octet_length(token_hash)=32),
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    email_address text NOT NULL CHECK (length(email_address) BETWEEN 3 AND 254 AND email_address=lower(email_address)
        AND position(chr(10) in email_address)=0 AND position(chr(13) in email_address)=0),
    password_revision bigint NOT NULL CHECK (password_revision>0),
    mfa_revision bigint NOT NULL CHECK (mfa_revision>0),
    mfa_enrolled_at timestamptz NOT NULL CHECK (isfinite(mfa_enrolled_at)),
    namespace text NOT NULL CHECK (namespace ~ '^[a-z][a-z0-9_-]{0,63}$'),
    recovery_id uuid NOT NULL,
    requested_at timestamptz NOT NULL CHECK (isfinite(requested_at)),
    ready_at timestamptz NOT NULL CHECK (isfinite(ready_at) AND ready_at=requested_at+interval '24 hours'),
    expires_at timestamptz NOT NULL CHECK (isfinite(expires_at) AND expires_at=ready_at+interval '24 hours'),
    consumed_at timestamptz CHECK (consumed_at IS NULL OR isfinite(consumed_at))
);
CREATE INDEX IF NOT EXISTS mfa_disable_tokens_expiry_idx ON mfa_disable_tokens(expires_at,token_hash);
CREATE INDEX IF NOT EXISTS mfa_disable_tokens_account_idx ON mfa_disable_tokens(account_id) WHERE consumed_at IS NULL;
ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT IF EXISTS platform_mail_producer_outbox_template_check;
ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT platform_mail_producer_outbox_template_check
    CHECK (template IN ('account_deletion_pending','email_verification','password_reset','magic_link','mfa_recovery_used','mfa_disable_request'));
ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT IF EXISTS platform_mail_producer_outbox_verification_check;
ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT platform_mail_producer_outbox_verification_check CHECK (
    (template='account_deletion_pending' AND deletion_requested_at IS NOT NULL AND restore_until IS NOT NULL
        AND verification_token_hash IS NULL AND octet_length(sealed_verification)=0 AND verification_key_id='' AND verification_origin='')
    OR (template='mfa_recovery_used' AND deletion_requested_at IS NULL AND restore_until IS NULL
        AND verification_token_hash IS NULL AND octet_length(sealed_verification)=0 AND verification_key_id='' AND verification_origin='')
    OR (template IN ('email_verification','password_reset','magic_link','mfa_disable_request') AND deletion_requested_at IS NULL AND restore_until IS NULL
        AND ((state IN ('queued','claimed') AND verification_token_hash IS NOT NULL AND octet_length(verification_token_hash)=32
            AND octet_length(sealed_verification) BETWEEN 64 AND 4096 AND verification_key_id ~ '^[0-9a-f]{64}$'
            AND length(verification_origin) BETWEEN 9 AND 2048 AND verification_origin ~ '^https://[^/?#[:space:]]+$')
        OR (state NOT IN ('queued','claimed') AND verification_token_hash IS NULL AND octet_length(sealed_verification)=0
            AND verification_key_id='' AND verification_origin='')))
);
CREATE INDEX IF NOT EXISTS platform_mail_producer_outbox_mfa_disable_keys_idx
    ON platform_mail_producer_outbox(namespace,producer_id,verification_key_id)
    WHERE template='mfa_disable_request' AND state IN ('queued','claimed');
ALTER TABLE platform_mail_outbox_reconciliations DROP CONSTRAINT IF EXISTS platform_mail_outbox_reconciliations_template_check;
ALTER TABLE platform_mail_outbox_reconciliations ADD CONSTRAINT platform_mail_outbox_reconciliations_template_check
    CHECK (template IN ('account_deletion_pending','email_verification','password_reset','magic_link','mfa_recovery_used','mfa_disable_request'));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
LOCK TABLE accounts,mfa_disable_requests,mfa_disable_tokens,platform_mail_producer_outbox,platform_mail_outbox_reconciliations IN ACCESS EXCLUSIVE MODE;
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM platform_mail_producer_outbox WHERE template='mfa_disable_request')
        OR EXISTS (SELECT 1 FROM platform_mail_outbox_reconciliations WHERE template='mfa_disable_request')
        OR EXISTS (SELECT 1 FROM mfa_disable_tokens) THEN
        RAISE EXCEPTION 'cannot discard MFA disable intent, credential binding or reconciliation history';
    END IF;
END $$;
ALTER TABLE platform_mail_outbox_reconciliations DROP CONSTRAINT IF EXISTS platform_mail_outbox_reconciliations_template_check;
ALTER TABLE platform_mail_outbox_reconciliations ADD CONSTRAINT platform_mail_outbox_reconciliations_template_check
    CHECK (template IN ('account_deletion_pending','email_verification','password_reset','magic_link','mfa_recovery_used'));
DROP INDEX IF EXISTS platform_mail_producer_outbox_mfa_disable_keys_idx;
ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT IF EXISTS platform_mail_producer_outbox_template_check;
ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT platform_mail_producer_outbox_template_check
    CHECK (template IN ('account_deletion_pending','email_verification','password_reset','magic_link','mfa_recovery_used'));
ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT IF EXISTS platform_mail_producer_outbox_verification_check;
ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT platform_mail_producer_outbox_verification_check CHECK (
    (template='account_deletion_pending' AND deletion_requested_at IS NOT NULL AND restore_until IS NOT NULL
        AND verification_token_hash IS NULL AND octet_length(sealed_verification)=0 AND verification_key_id='' AND verification_origin='')
    OR (template='mfa_recovery_used' AND deletion_requested_at IS NULL AND restore_until IS NULL
        AND verification_token_hash IS NULL AND octet_length(sealed_verification)=0 AND verification_key_id='' AND verification_origin='')
    OR (template IN ('email_verification','password_reset','magic_link') AND deletion_requested_at IS NULL AND restore_until IS NULL
        AND ((state IN ('queued','claimed') AND verification_token_hash IS NOT NULL AND octet_length(verification_token_hash)=32
            AND octet_length(sealed_verification) BETWEEN 64 AND 4096 AND verification_key_id ~ '^[0-9a-f]{64}$'
            AND length(verification_origin) BETWEEN 9 AND 2048 AND verification_origin ~ '^https://[^/?#[:space:]]+$')
        OR (state NOT IN ('queued','claimed') AND verification_token_hash IS NULL AND octet_length(sealed_verification)=0
            AND verification_key_id='' AND verification_origin='')))
);
DROP TABLE IF EXISTS mfa_disable_tokens;
ALTER TABLE accounts DROP COLUMN IF EXISTS mfa_revision;
-- +goose StatementEnd
