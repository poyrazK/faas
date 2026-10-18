-- filename: 20261017115753852_platform_mail_account_suspended_outbox.sql

-- +goose Up
-- +goose StatementBegin
ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT IF EXISTS platform_mail_producer_outbox_template_check;
ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT platform_mail_producer_outbox_template_check
    CHECK (template IN ('account_deletion_pending','email_verification','password_reset','magic_link','mfa_recovery_used','mfa_disable_request','mfa_disabled','password_changed','mfa_enabled','payment_failed','account_restored','account_suspended'));
ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT IF EXISTS platform_mail_producer_outbox_verification_check;
ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT platform_mail_producer_outbox_verification_check CHECK (
    (template='account_deletion_pending' AND deletion_requested_at IS NOT NULL AND restore_until IS NOT NULL
        AND verification_token_hash IS NULL AND octet_length(sealed_verification)=0 AND verification_key_id='' AND verification_origin='')
    OR (template IN ('mfa_recovery_used','mfa_disabled','password_changed','mfa_enabled','payment_failed','account_restored','account_suspended') AND deletion_requested_at IS NULL AND restore_until IS NULL
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
    (template NOT IN ('mfa_recovery_used','mfa_disabled','password_changed','mfa_enabled','payment_failed','account_restored','account_suspended') AND octet_length(sealed_security_alert)=0 AND security_alert_key_id='')
    OR (template IN ('mfa_recovery_used','mfa_disabled','password_changed','mfa_enabled','payment_failed','account_restored','account_suspended') AND
        ((state IN ('queued','claimed') AND octet_length(sealed_security_alert) BETWEEN 64 AND 4096 AND security_alert_key_id ~ '^[0-9a-f]{64}$')
        OR (state NOT IN ('queued','claimed') AND octet_length(sealed_security_alert)=0 AND security_alert_key_id='')))
);
ALTER TABLE platform_mail_outbox_reconciliations DROP CONSTRAINT IF EXISTS platform_mail_outbox_reconciliations_template_check;
ALTER TABLE platform_mail_outbox_reconciliations ADD CONSTRAINT platform_mail_outbox_reconciliations_template_check
    CHECK (template IN ('account_deletion_pending','email_verification','password_reset','magic_link','mfa_recovery_used','mfa_disable_request','mfa_disabled','password_changed','mfa_enabled','payment_failed','account_restored','account_suspended'));
ALTER TABLE platform_mail_producer_outbox ADD COLUMN IF NOT EXISTS suspension_past_due_at timestamptz;
ALTER TABLE platform_mail_producer_outbox ADD COLUMN IF NOT EXISTS account_suspended_revision bigint;
ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT IF EXISTS platform_mail_producer_outbox_suspended_check;
ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT platform_mail_producer_outbox_suspended_check CHECK (
    (template='account_suspended' AND suspension_past_due_at IS NOT NULL AND isfinite(suspension_past_due_at)
        AND account_suspended_revision IS NOT NULL AND account_suspended_revision>0)
    OR (template<>'account_suspended' AND suspension_past_due_at IS NULL AND account_suspended_revision IS NULL)
);
CREATE UNIQUE INDEX IF NOT EXISTS platform_mail_producer_outbox_suspended_period_idx
    ON platform_mail_producer_outbox(account_id,suspension_past_due_at) WHERE template='account_suspended';
CREATE INDEX IF NOT EXISTS platform_mail_producer_outbox_account_suspended_keys_idx
    ON platform_mail_producer_outbox(namespace,producer_id,security_alert_key_id)
    WHERE template='account_suspended' AND state IN ('queued','claimed');
-- Private billing-command receipt, also retaining meterd's follow-up lease.
-- It carries no recipient, message body or encryption/decryption material.
CREATE TABLE IF NOT EXISTS billing_account_suspensions (
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    past_due_at timestamptz NOT NULL CHECK(isfinite(past_due_at)),
    intent_id uuid NOT NULL UNIQUE REFERENCES platform_mail_producer_outbox(id) ON DELETE CASCADE,
    suspended_at timestamptz NOT NULL CHECK(isfinite(suspended_at)),
    billing_revision bigint NOT NULL CHECK(billing_revision>0),
    followup_completed_at timestamptz CHECK(followup_completed_at IS NULL OR isfinite(followup_completed_at)),
    lease_id uuid CHECK(lease_id IS NULL OR lease_id<>'00000000-0000-0000-0000-000000000000'::uuid),
    lease_until timestamptz CHECK(lease_until IS NULL OR isfinite(lease_until)),
    PRIMARY KEY(account_id,past_due_at),
    CHECK((lease_id IS NULL)=(lease_until IS NULL)),
    CHECK(followup_completed_at IS NULL OR lease_id IS NOT NULL)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
LOCK TABLE platform_mail_producer_outbox,platform_mail_outbox_reconciliations,billing_account_suspensions IN ACCESS EXCLUSIVE MODE;
DO $$
BEGIN
    IF EXISTS(SELECT 1 FROM platform_mail_producer_outbox WHERE template='account_suspended')
        OR EXISTS(SELECT 1 FROM platform_mail_outbox_reconciliations WHERE template='account_suspended')
        OR EXISTS(SELECT 1 FROM billing_account_suspensions) THEN
        RAISE EXCEPTION 'cannot discard account suspension intent, follow-up receipt or reconciliation history';
    END IF;
END $$;
DROP TABLE billing_account_suspensions;
DROP INDEX IF EXISTS platform_mail_producer_outbox_suspended_period_idx;
DROP INDEX IF EXISTS platform_mail_producer_outbox_account_suspended_keys_idx;
ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT IF EXISTS platform_mail_producer_outbox_suspended_check;
ALTER TABLE platform_mail_producer_outbox DROP COLUMN IF EXISTS suspension_past_due_at;
ALTER TABLE platform_mail_producer_outbox DROP COLUMN IF EXISTS account_suspended_revision;
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
ALTER TABLE platform_mail_outbox_reconciliations DROP CONSTRAINT IF EXISTS platform_mail_outbox_reconciliations_template_check;
ALTER TABLE platform_mail_outbox_reconciliations ADD CONSTRAINT platform_mail_outbox_reconciliations_template_check
    CHECK (template IN ('account_deletion_pending','email_verification','password_reset','magic_link','mfa_recovery_used','mfa_disable_request','mfa_disabled','password_changed','mfa_enabled','payment_failed','account_restored'));
-- +goose StatementEnd
