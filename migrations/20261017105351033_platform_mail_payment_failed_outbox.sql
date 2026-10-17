-- filename: 20261017105351033_platform_mail_payment_failed_outbox.sql

-- +goose Up
-- +goose StatementBegin
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
ALTER TABLE platform_mail_producer_outbox ADD COLUMN IF NOT EXISTS payment_past_due_at timestamptz;
ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT IF EXISTS platform_mail_producer_outbox_payment_check;
ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT platform_mail_producer_outbox_payment_check CHECK (
    (template='payment_failed' AND payment_past_due_at IS NOT NULL AND isfinite(payment_past_due_at) AND expires_at>payment_past_due_at)
    OR (template<>'payment_failed' AND payment_past_due_at IS NULL)
);
CREATE UNIQUE INDEX IF NOT EXISTS platform_mail_producer_outbox_payment_episode_idx
    ON platform_mail_producer_outbox(account_id,payment_past_due_at) WHERE template='payment_failed';
-- Permanent, minimal application receipts. Pruning them would let a provider
-- replay re-enter dunning after payment recovery. Account erasure cascades.
CREATE TABLE IF NOT EXISTS billing_payment_failure_receipts (
    provider text NOT NULL CHECK (provider IN ('stripe','paddle','polar')),
    event_id text NOT NULL CHECK (length(event_id) BETWEEN 1 AND 256 AND position(chr(10) in event_id)=0 AND position(chr(13) in event_id)=0),
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    event_type text NOT NULL CHECK (event_type IN ('payment_failed','subscription_past_due')),
    processed_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK (isfinite(processed_at)),
    PRIMARY KEY(provider,event_id)
);
CREATE INDEX IF NOT EXISTS billing_payment_failure_receipts_account_idx ON billing_payment_failure_receipts(account_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
LOCK TABLE platform_mail_producer_outbox,platform_mail_outbox_reconciliations,billing_payment_failure_receipts IN ACCESS EXCLUSIVE MODE;
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM platform_mail_producer_outbox WHERE template='payment_failed')
        OR EXISTS (SELECT 1 FROM platform_mail_outbox_reconciliations WHERE template='payment_failed')
        OR EXISTS (SELECT 1 FROM billing_payment_failure_receipts) THEN
        RAISE EXCEPTION 'cannot discard payment failure intent, receipt or reconciliation history';
    END IF;
END $$;
DROP TABLE billing_payment_failure_receipts;
DROP INDEX IF EXISTS platform_mail_producer_outbox_payment_failed_keys_idx;
DROP INDEX IF EXISTS platform_mail_producer_outbox_payment_episode_idx;
ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT IF EXISTS platform_mail_producer_outbox_payment_check;
ALTER TABLE platform_mail_producer_outbox DROP COLUMN IF EXISTS payment_past_due_at;
ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT IF EXISTS platform_mail_producer_outbox_template_check;
ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT platform_mail_producer_outbox_template_check
    CHECK (template IN ('account_deletion_pending','email_verification','password_reset','magic_link','mfa_recovery_used','mfa_disable_request','mfa_disabled','password_changed','mfa_enabled'));
ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT IF EXISTS platform_mail_producer_outbox_verification_check;
ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT platform_mail_producer_outbox_verification_check CHECK (
    (template='account_deletion_pending' AND deletion_requested_at IS NOT NULL AND restore_until IS NOT NULL
        AND verification_token_hash IS NULL AND octet_length(sealed_verification)=0 AND verification_key_id='' AND verification_origin='')
    OR (template IN ('mfa_recovery_used','mfa_disabled','password_changed','mfa_enabled') AND deletion_requested_at IS NULL AND restore_until IS NULL
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
    (template NOT IN ('mfa_recovery_used','mfa_disabled','password_changed','mfa_enabled') AND octet_length(sealed_security_alert)=0 AND security_alert_key_id='')
    OR (template IN ('mfa_recovery_used','mfa_disabled','password_changed','mfa_enabled') AND
        ((state IN ('queued','claimed') AND octet_length(sealed_security_alert) BETWEEN 64 AND 4096 AND security_alert_key_id ~ '^[0-9a-f]{64}$')
        OR (state NOT IN ('queued','claimed') AND octet_length(sealed_security_alert)=0 AND security_alert_key_id='')))
);
CREATE INDEX IF NOT EXISTS platform_mail_producer_outbox_mfa_enabled_keys_idx
    ON platform_mail_producer_outbox(namespace,producer_id,security_alert_key_id)
    WHERE template='mfa_enabled' AND state IN ('queued','claimed');
ALTER TABLE platform_mail_outbox_reconciliations DROP CONSTRAINT IF EXISTS platform_mail_outbox_reconciliations_template_check;
ALTER TABLE platform_mail_outbox_reconciliations ADD CONSTRAINT platform_mail_outbox_reconciliations_template_check
    CHECK (template IN ('account_deletion_pending','email_verification','password_reset','magic_link','mfa_recovery_used','mfa_disable_request','mfa_disabled','password_changed','mfa_enabled'));
-- +goose StatementEnd
