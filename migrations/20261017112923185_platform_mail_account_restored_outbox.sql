-- filename: 20261017112923185_platform_mail_account_restored_outbox.sql

-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT IF EXISTS platform_mail_producer_outbox_template_check;
ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT platform_mail_producer_outbox_template_check
    CHECK (template IN ('account_deletion_pending','email_verification','password_reset','magic_link','mfa_recovery_used','mfa_disable_request','mfa_disabled','password_changed','mfa_enabled','payment_failed','account_restored'));
ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT IF EXISTS platform_mail_producer_outbox_verification_check;
ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT platform_mail_producer_outbox_verification_check CHECK (
    (template='account_deletion_pending' AND deletion_requested_at IS NOT NULL AND restore_until IS NOT NULL
        AND verification_token_hash IS NULL AND octet_length(sealed_verification)=0 AND verification_key_id='' AND verification_origin='')
    OR (template IN ('mfa_recovery_used','mfa_disabled','password_changed','mfa_enabled','payment_failed','account_restored') AND deletion_requested_at IS NULL AND restore_until IS NULL
        AND verification_token_hash IS NULL AND octet_length(sealed_verification)=0 AND verification_key_id='' AND verification_origin='')
    OR (template IN ('email_verification','password_reset','magic_link','mfa_disable_request') AND deletion_requested_at IS NULL AND restore_until IS NULL
        AND ((state IN ('queued','claimed') AND verification_token_hash IS NOT NULL AND octet_length(verification_token_hash)=32
            AND octet_length(sealed_verification) BETWEEN 64 AND 4096 AND verification_key_id ~ '^[0-9a-f]{64}$'
            AND length(verification_origin) BETWEEN 9 AND 2048 AND verification_origin ~ '^https://[^/?#[:space:]]+$')
        OR (state NOT IN ('queued','claimed') AND verification_token_hash IS NULL AND octet_length(sealed_verification)=0
            AND verification_key_id='' AND verification_origin='')))
);
ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT IF EXISTS platform_mail_producer_outbox_security_alert_check;
ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT platform_mail_producer_outbox_security_alert_check CHECK (
    (template NOT IN ('mfa_recovery_used','mfa_disabled','password_changed','mfa_enabled','payment_failed','account_restored') AND octet_length(sealed_security_alert)=0 AND security_alert_key_id='')
    OR (template IN ('mfa_recovery_used','mfa_disabled','password_changed','mfa_enabled','payment_failed','account_restored') AND
        ((state IN ('queued','claimed') AND octet_length(sealed_security_alert) BETWEEN 64 AND 4096 AND security_alert_key_id ~ '^[0-9a-f]{64}$')
        OR (state NOT IN ('queued','claimed') AND octet_length(sealed_security_alert)=0 AND security_alert_key_id='')))
);
CREATE INDEX IF NOT EXISTS platform_mail_producer_outbox_account_restored_keys_idx
    ON platform_mail_producer_outbox(namespace,producer_id,security_alert_key_id)
    WHERE template='account_restored' AND state IN ('queued','claimed');
ALTER TABLE platform_mail_outbox_reconciliations DROP CONSTRAINT IF EXISTS platform_mail_outbox_reconciliations_template_check;
ALTER TABLE platform_mail_outbox_reconciliations ADD CONSTRAINT platform_mail_outbox_reconciliations_template_check
    CHECK (template IN ('account_deletion_pending','email_verification','password_reset','magic_link','mfa_recovery_used','mfa_disable_request','mfa_disabled','password_changed','mfa_enabled','payment_failed','account_restored'));
-- Persisted confirmation bindings must remain invalidated by every account
-- lifecycle writer, including meterd, legacy billing and a flag rollback.
ALTER TABLE accounts ADD COLUMN IF NOT EXISTS billing_mail_revision bigint NOT NULL DEFAULT 1 CHECK (billing_mail_revision>0);
CREATE OR REPLACE FUNCTION advance_account_billing_mail_revision() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.status IS DISTINCT FROM OLD.status OR NEW.past_due_at IS DISTINCT FROM OLD.past_due_at
        OR NEW.deletion_requested_at IS DISTINCT FROM OLD.deletion_requested_at OR NEW.email IS DISTINCT FROM OLD.email THEN
        NEW.billing_mail_revision := OLD.billing_mail_revision+1;
    ELSE
        NEW.billing_mail_revision := OLD.billing_mail_revision;
    END IF;
    RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS accounts_billing_mail_revision_guard ON accounts;
CREATE TRIGGER accounts_billing_mail_revision_guard
    BEFORE UPDATE OF status,past_due_at,deletion_requested_at,email,billing_mail_revision ON accounts
    FOR EACH ROW EXECUTE FUNCTION advance_account_billing_mail_revision();
ALTER TABLE platform_mail_producer_outbox ADD COLUMN IF NOT EXISTS account_restored_revision bigint;
ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT IF EXISTS platform_mail_producer_outbox_restored_check;
ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT platform_mail_producer_outbox_restored_check CHECK (
    (template='account_restored' AND account_restored_revision IS NOT NULL AND account_restored_revision>0)
    OR (template<>'account_restored' AND account_restored_revision IS NULL)
);
CREATE UNIQUE INDEX IF NOT EXISTS platform_mail_producer_outbox_restored_revision_idx
    ON platform_mail_producer_outbox(account_id,account_restored_revision) WHERE template='account_restored';
CREATE TABLE IF NOT EXISTS billing_payment_recovery_receipts (
    provider text NOT NULL CHECK (provider IN ('stripe','paddle','polar')),
    event_id text NOT NULL CHECK (length(event_id) BETWEEN 1 AND 256 AND position(chr(10) in event_id)=0 AND position(chr(13) in event_id)=0),
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    processed_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK (isfinite(processed_at)),
    PRIMARY KEY(provider,event_id)
);
CREATE INDEX IF NOT EXISTS billing_payment_recovery_receipts_account_idx ON billing_payment_recovery_receipts(account_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
LOCK TABLE platform_mail_producer_outbox,platform_mail_outbox_reconciliations,billing_payment_recovery_receipts,accounts IN ACCESS EXCLUSIVE MODE;
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM platform_mail_producer_outbox WHERE template='account_restored')
        OR EXISTS (SELECT 1 FROM platform_mail_outbox_reconciliations WHERE template='account_restored')
        OR EXISTS (SELECT 1 FROM billing_payment_recovery_receipts) THEN
        RAISE EXCEPTION 'cannot discard account restoration intent, receipt or reconciliation history';
    END IF;
END $$;
DROP TABLE billing_payment_recovery_receipts;
DROP INDEX IF EXISTS platform_mail_producer_outbox_account_restored_keys_idx;
DROP INDEX IF EXISTS platform_mail_producer_outbox_restored_revision_idx;
ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT IF EXISTS platform_mail_producer_outbox_restored_check;
ALTER TABLE platform_mail_producer_outbox DROP COLUMN IF EXISTS account_restored_revision;
DROP TRIGGER IF EXISTS accounts_billing_mail_revision_guard ON accounts;
DROP FUNCTION IF EXISTS advance_account_billing_mail_revision();
ALTER TABLE accounts DROP COLUMN IF EXISTS billing_mail_revision;
ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT IF EXISTS platform_mail_producer_outbox_template_check;
ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT platform_mail_producer_outbox_template_check
    CHECK (template IN ('account_deletion_pending','email_verification','password_reset','magic_link','mfa_recovery_used','mfa_disable_request','mfa_disabled','password_changed','mfa_enabled','payment_failed'));
ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT IF EXISTS platform_mail_producer_outbox_verification_check;
ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT platform_mail_producer_outbox_verification_check CHECK (
    (template='account_deletion_pending' AND deletion_requested_at IS NOT NULL AND restore_until IS NOT NULL
        AND verification_token_hash IS NULL AND octet_length(sealed_verification)=0 AND verification_key_id='' AND verification_origin='')
    OR (template IN ('mfa_recovery_used','mfa_disabled','password_changed','mfa_enabled','payment_failed') AND deletion_requested_at IS NULL AND restore_until IS NULL
        AND verification_token_hash IS NULL AND octet_length(sealed_verification)=0 AND verification_key_id='' AND verification_origin='')
    OR (template IN ('email_verification','password_reset','magic_link','mfa_disable_request') AND deletion_requested_at IS NULL AND restore_until IS NULL
        AND ((state IN ('queued','claimed') AND verification_token_hash IS NOT NULL AND octet_length(verification_token_hash)=32
            AND octet_length(sealed_verification) BETWEEN 64 AND 4096 AND verification_key_id ~ '^[0-9a-f]{64}$'
            AND length(verification_origin) BETWEEN 9 AND 2048 AND verification_origin ~ '^https://[^/?#[:space:]]+$')
        OR (state NOT IN ('queued','claimed') AND verification_token_hash IS NULL AND octet_length(sealed_verification)=0
            AND verification_key_id='' AND verification_origin='')))
);
ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT IF EXISTS platform_mail_producer_outbox_security_alert_check;
ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT platform_mail_producer_outbox_security_alert_check CHECK (
    (template NOT IN ('mfa_recovery_used','mfa_disabled','password_changed','mfa_enabled','payment_failed') AND octet_length(sealed_security_alert)=0 AND security_alert_key_id='')
    OR (template IN ('mfa_recovery_used','mfa_disabled','password_changed','mfa_enabled','payment_failed') AND
        ((state IN ('queued','claimed') AND octet_length(sealed_security_alert) BETWEEN 64 AND 4096 AND security_alert_key_id ~ '^[0-9a-f]{64}$')
        OR (state NOT IN ('queued','claimed') AND octet_length(sealed_security_alert)=0 AND security_alert_key_id='')))
);
CREATE INDEX IF NOT EXISTS platform_mail_producer_outbox_payment_failed_keys_idx
    ON platform_mail_producer_outbox(namespace,producer_id,security_alert_key_id)
    WHERE template='payment_failed' AND state IN ('queued','claimed');
ALTER TABLE platform_mail_outbox_reconciliations DROP CONSTRAINT IF EXISTS platform_mail_outbox_reconciliations_template_check;
ALTER TABLE platform_mail_outbox_reconciliations ADD CONSTRAINT platform_mail_outbox_reconciliations_template_check
    CHECK (template IN ('account_deletion_pending','email_verification','password_reset','magic_link','mfa_recovery_used','mfa_disable_request','mfa_disabled','password_changed','mfa_enabled','payment_failed'));
-- +goose StatementEnd
