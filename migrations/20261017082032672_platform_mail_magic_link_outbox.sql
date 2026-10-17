-- filename: 20261017082032672_platform_mail_magic_link_outbox.sql

-- +goose Up
-- +goose StatementBegin
-- Purpose-specific sign-in credentials never enter login_tokens or reset tokens.
-- password_reset_revision is the shared password mutation counter, including
-- writes while every mail flag is off. Verification state is bound separately.
CREATE TABLE IF NOT EXISTS magic_link_tokens (
    token_hash bytea PRIMARY KEY CHECK (octet_length(token_hash)=32),
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    email_address text NOT NULL CHECK (length(email_address) BETWEEN 3 AND 254 AND email_address=lower(email_address)
        AND position(chr(10) in email_address)=0 AND position(chr(13) in email_address)=0),
    password_revision bigint NOT NULL CHECK (password_revision>0),
    email_verified_at timestamptz CHECK (email_verified_at IS NULL OR isfinite(email_verified_at)),
    namespace text NOT NULL CHECK (namespace ~ '^[a-z][a-z0-9_-]{0,63}$'),
    recovery_id uuid NOT NULL,
    expires_at timestamptz NOT NULL CHECK (isfinite(expires_at)),
    consumed_at timestamptz CHECK (consumed_at IS NULL OR isfinite(consumed_at)),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK (isfinite(created_at) AND expires_at>created_at)
);
CREATE INDEX IF NOT EXISTS magic_link_tokens_expiry_idx ON magic_link_tokens(expires_at,token_hash);
ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT IF EXISTS platform_mail_producer_outbox_template_check;
ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT platform_mail_producer_outbox_template_check
    CHECK (template IN ('account_deletion_pending','email_verification','password_reset','magic_link'));
ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT IF EXISTS platform_mail_producer_outbox_verification_check;
ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT platform_mail_producer_outbox_verification_check CHECK (
    (template='account_deletion_pending' AND deletion_requested_at IS NOT NULL AND restore_until IS NOT NULL
        AND verification_token_hash IS NULL AND octet_length(sealed_verification)=0 AND verification_key_id='' AND verification_origin='')
    OR (template IN ('email_verification','password_reset','magic_link') AND deletion_requested_at IS NULL AND restore_until IS NULL
        AND ((state IN ('queued','claimed') AND verification_token_hash IS NOT NULL AND octet_length(verification_token_hash)=32
            AND octet_length(sealed_verification) BETWEEN 64 AND 4096 AND verification_key_id ~ '^[0-9a-f]{64}$'
            AND length(verification_origin) BETWEEN 9 AND 2048 AND verification_origin ~ '^https://[^/?#[:space:]]+$')
        OR (state NOT IN ('queued','claimed') AND verification_token_hash IS NULL AND octet_length(sealed_verification)=0
            AND verification_key_id='' AND verification_origin='')))
);
CREATE INDEX IF NOT EXISTS platform_mail_producer_outbox_magic_keys_idx
    ON platform_mail_producer_outbox(namespace,producer_id,verification_key_id)
    WHERE template='magic_link' AND state IN ('queued','claimed');
ALTER TABLE platform_mail_outbox_reconciliations DROP CONSTRAINT IF EXISTS platform_mail_outbox_reconciliations_template_check;
ALTER TABLE platform_mail_outbox_reconciliations ADD CONSTRAINT platform_mail_outbox_reconciliations_template_check
    CHECK (template IN ('account_deletion_pending','email_verification','password_reset','magic_link'));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
LOCK TABLE accounts,platform_mail_producer_outbox,platform_mail_outbox_reconciliations,magic_link_tokens IN ACCESS EXCLUSIVE MODE;
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM platform_mail_producer_outbox WHERE template='magic_link')
        OR EXISTS (SELECT 1 FROM platform_mail_outbox_reconciliations WHERE template='magic_link')
        OR EXISTS (SELECT 1 FROM magic_link_tokens) THEN
        RAISE EXCEPTION 'cannot discard magic-link intent, credential binding or reconciliation history';
    END IF;
END $$;
ALTER TABLE platform_mail_outbox_reconciliations DROP CONSTRAINT IF EXISTS platform_mail_outbox_reconciliations_template_check;
ALTER TABLE platform_mail_outbox_reconciliations ADD CONSTRAINT platform_mail_outbox_reconciliations_template_check
    CHECK (template IN ('account_deletion_pending','email_verification','password_reset'));
DROP INDEX IF EXISTS platform_mail_producer_outbox_magic_keys_idx;
ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT IF EXISTS platform_mail_producer_outbox_template_check;
ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT platform_mail_producer_outbox_template_check
    CHECK (template IN ('account_deletion_pending','email_verification','password_reset'));
ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT IF EXISTS platform_mail_producer_outbox_verification_check;
ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT platform_mail_producer_outbox_verification_check CHECK (
    (template='account_deletion_pending' AND deletion_requested_at IS NOT NULL AND restore_until IS NOT NULL
        AND verification_token_hash IS NULL AND octet_length(sealed_verification)=0 AND verification_key_id='' AND verification_origin='')
    OR (template IN ('email_verification','password_reset') AND deletion_requested_at IS NULL AND restore_until IS NULL
        AND ((state IN ('queued','claimed') AND verification_token_hash IS NOT NULL AND octet_length(verification_token_hash)=32
            AND octet_length(sealed_verification) BETWEEN 64 AND 4096 AND verification_key_id ~ '^[0-9a-f]{64}$'
            AND length(verification_origin) BETWEEN 9 AND 2048 AND verification_origin ~ '^https://[^/?#[:space:]]+$')
        OR (state NOT IN ('queued','claimed') AND verification_token_hash IS NULL AND octet_length(sealed_verification)=0
            AND verification_key_id='' AND verification_origin='')))
);
DROP TABLE IF EXISTS magic_link_tokens;
-- +goose StatementEnd
