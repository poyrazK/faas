-- filename: 20261017133739854_platform_mail_quota_warning_outbox.sql

-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT IF EXISTS platform_mail_producer_outbox_template_check;
ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT platform_mail_producer_outbox_template_check
    CHECK (template IN ('account_deletion_pending','email_verification','password_reset','magic_link','mfa_recovery_used','mfa_disable_request','mfa_disabled','password_changed','mfa_enabled','payment_failed','account_restored','account_suspended','nonpayment_deletion','quota_warning'));
ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT IF EXISTS platform_mail_producer_outbox_verification_check;
ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT platform_mail_producer_outbox_verification_check CHECK (
    (template='account_deletion_pending' AND deletion_requested_at IS NOT NULL AND restore_until IS NOT NULL
        AND verification_token_hash IS NULL AND octet_length(sealed_verification)=0 AND verification_key_id='' AND verification_origin='')
    OR (template IN ('mfa_recovery_used','mfa_disabled','password_changed','mfa_enabled','payment_failed','account_restored','account_suspended','nonpayment_deletion','quota_warning') AND deletion_requested_at IS NULL AND restore_until IS NULL
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
    (template NOT IN ('mfa_recovery_used','mfa_disabled','password_changed','mfa_enabled','payment_failed','account_restored','account_suspended','nonpayment_deletion','quota_warning') AND octet_length(sealed_security_alert)=0 AND security_alert_key_id='')
    OR (template IN ('mfa_recovery_used','mfa_disabled','password_changed','mfa_enabled','payment_failed','account_restored','account_suspended','nonpayment_deletion','quota_warning') AND
        ((state IN ('queued','claimed') AND octet_length(sealed_security_alert) BETWEEN 64 AND 4096 AND security_alert_key_id ~ '^[0-9a-f]{64}$')
        OR (state NOT IN ('queued','claimed') AND octet_length(sealed_security_alert)=0 AND security_alert_key_id='')))
);
ALTER TABLE platform_mail_outbox_reconciliations DROP CONSTRAINT IF EXISTS platform_mail_outbox_reconciliations_template_check;
ALTER TABLE platform_mail_outbox_reconciliations ADD CONSTRAINT platform_mail_outbox_reconciliations_template_check
    CHECK (template IN ('account_deletion_pending','email_verification','password_reset','magic_link','mfa_recovery_used','mfa_disable_request','mfa_disabled','password_changed','mfa_enabled','payment_failed','account_restored','account_suspended','nonpayment_deletion','quota_warning'));
ALTER TABLE accounts ADD COLUMN IF NOT EXISTS quota_mail_revision bigint NOT NULL DEFAULT 1 CHECK(quota_mail_revision>0);
CREATE OR REPLACE FUNCTION advance_account_quota_mail_revision() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.plan IS DISTINCT FROM OLD.plan OR NEW.email IS DISTINCT FROM OLD.email
        OR NEW.status IS DISTINCT FROM OLD.status OR NEW.past_due_at IS DISTINCT FROM OLD.past_due_at
        OR NEW.deletion_requested_at IS DISTINCT FROM OLD.deletion_requested_at
        OR NEW.last_quota_warning_at IS DISTINCT FROM OLD.last_quota_warning_at THEN
        NEW.quota_mail_revision := OLD.quota_mail_revision+1;
    ELSE
        NEW.quota_mail_revision := OLD.quota_mail_revision;
    END IF;
    RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS accounts_quota_mail_revision_guard ON accounts;
CREATE TRIGGER accounts_quota_mail_revision_guard
    BEFORE UPDATE OF plan,email,status,past_due_at,deletion_requested_at,last_quota_warning_at,quota_mail_revision ON accounts
    FOR EACH ROW EXECUTE FUNCTION advance_account_quota_mail_revision();
ALTER TABLE platform_mail_producer_outbox ADD COLUMN IF NOT EXISTS quota_warning_day timestamptz;
ALTER TABLE platform_mail_producer_outbox ADD COLUMN IF NOT EXISTS quota_warning_plan text;
ALTER TABLE platform_mail_producer_outbox ADD COLUMN IF NOT EXISTS quota_warning_revision bigint;
ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT IF EXISTS platform_mail_producer_outbox_quota_warning_check;
ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT platform_mail_producer_outbox_quota_warning_check CHECK (
    (template='quota_warning' AND quota_warning_day IS NOT NULL AND isfinite(quota_warning_day)
        AND quota_warning_day=(date_trunc('day',quota_warning_day AT TIME ZONE 'UTC') AT TIME ZONE 'UTC')
        AND quota_warning_plan IS NOT NULL AND quota_warning_plan IN ('hobby','pro','scale')
        AND quota_warning_revision IS NOT NULL AND quota_warning_revision>0)
    OR (template<>'quota_warning' AND quota_warning_day IS NULL AND quota_warning_plan IS NULL AND quota_warning_revision IS NULL)
);
CREATE UNIQUE INDEX IF NOT EXISTS platform_mail_producer_outbox_quota_warning_day_idx
    ON platform_mail_producer_outbox(account_id,quota_warning_day) WHERE template='quota_warning';
CREATE INDEX IF NOT EXISTS platform_mail_producer_outbox_quota_warning_keys_idx
    ON platform_mail_producer_outbox(namespace,producer_id,security_alert_key_id)
    WHERE template='quota_warning' AND state IN ('queued','claimed');
-- Business snapshot for meterd's notification retry, not a mail body or recipient.
CREATE TABLE IF NOT EXISTS billing_quota_warnings (
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    warning_day timestamptz NOT NULL CHECK(isfinite(warning_day) AND warning_day=(date_trunc('day',warning_day AT TIME ZONE 'UTC') AT TIME ZONE 'UTC')),
    intent_id uuid NOT NULL UNIQUE REFERENCES platform_mail_producer_outbox(id) ON DELETE CASCADE,
    observed_at timestamptz NOT NULL CHECK(isfinite(observed_at) AND observed_at>=warning_day AND observed_at<warning_day+interval '24 hours'),
    plan text NOT NULL CHECK(plan IN ('hobby','pro','scale')),
    used_mb_seconds bigint NOT NULL CHECK(used_mb_seconds>=0),
    quota_gb integer NOT NULL CHECK(quota_gb>0),
    quota_revision bigint NOT NULL CHECK(quota_revision>0),
    followup_completed_at timestamptz CHECK(followup_completed_at IS NULL OR isfinite(followup_completed_at)),
    lease_id uuid CHECK(lease_id IS NULL OR lease_id<>'00000000-0000-0000-0000-000000000000'::uuid),
    lease_until timestamptz CHECK(lease_until IS NULL OR isfinite(lease_until)),
    PRIMARY KEY(account_id,warning_day),
    CHECK((lease_id IS NULL)=(lease_until IS NULL)),
    CHECK(followup_completed_at IS NULL OR lease_id IS NOT NULL)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
LOCK TABLE platform_mail_producer_outbox,platform_mail_outbox_reconciliations,billing_quota_warnings,accounts IN ACCESS EXCLUSIVE MODE;
DO $$
BEGIN
    IF EXISTS(SELECT 1 FROM platform_mail_producer_outbox WHERE template='quota_warning')
        OR EXISTS(SELECT 1 FROM platform_mail_outbox_reconciliations WHERE template='quota_warning')
        OR EXISTS(SELECT 1 FROM billing_quota_warnings) THEN
        RAISE EXCEPTION 'cannot discard quota warning intent, command receipt or reconciliation history';
    END IF;
END $$;
DROP TABLE billing_quota_warnings;
DROP INDEX platform_mail_producer_outbox_quota_warning_day_idx;
DROP INDEX platform_mail_producer_outbox_quota_warning_keys_idx;
ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT platform_mail_producer_outbox_quota_warning_check;
ALTER TABLE platform_mail_producer_outbox DROP COLUMN quota_warning_day;
ALTER TABLE platform_mail_producer_outbox DROP COLUMN quota_warning_plan;
ALTER TABLE platform_mail_producer_outbox DROP COLUMN quota_warning_revision;
DROP TRIGGER accounts_quota_mail_revision_guard ON accounts;
DROP FUNCTION advance_account_quota_mail_revision();
ALTER TABLE accounts DROP COLUMN quota_mail_revision;
ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT IF EXISTS platform_mail_producer_outbox_template_check;
ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT platform_mail_producer_outbox_template_check
    CHECK (template IN ('account_deletion_pending','email_verification','password_reset','magic_link','mfa_recovery_used','mfa_disable_request','mfa_disabled','password_changed','mfa_enabled','payment_failed','account_restored','account_suspended','nonpayment_deletion'));
ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT IF EXISTS platform_mail_producer_outbox_verification_check;
ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT platform_mail_producer_outbox_verification_check CHECK (
    (template='account_deletion_pending' AND deletion_requested_at IS NOT NULL AND restore_until IS NOT NULL
        AND verification_token_hash IS NULL AND octet_length(sealed_verification)=0 AND verification_key_id='' AND verification_origin='')
    OR (template IN ('mfa_recovery_used','mfa_disabled','password_changed','mfa_enabled','payment_failed','account_restored','account_suspended','nonpayment_deletion') AND deletion_requested_at IS NULL AND restore_until IS NULL
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
    (template NOT IN ('mfa_recovery_used','mfa_disabled','password_changed','mfa_enabled','payment_failed','account_restored','account_suspended','nonpayment_deletion') AND octet_length(sealed_security_alert)=0 AND security_alert_key_id='')
    OR (template IN ('mfa_recovery_used','mfa_disabled','password_changed','mfa_enabled','payment_failed','account_restored','account_suspended','nonpayment_deletion') AND
        ((state IN ('queued','claimed') AND octet_length(sealed_security_alert) BETWEEN 64 AND 4096 AND security_alert_key_id ~ '^[0-9a-f]{64}$')
        OR (state NOT IN ('queued','claimed') AND octet_length(sealed_security_alert)=0 AND security_alert_key_id='')))
);
ALTER TABLE platform_mail_outbox_reconciliations DROP CONSTRAINT IF EXISTS platform_mail_outbox_reconciliations_template_check;
ALTER TABLE platform_mail_outbox_reconciliations ADD CONSTRAINT platform_mail_outbox_reconciliations_template_check
    CHECK (template IN ('account_deletion_pending','email_verification','password_reset','magic_link','mfa_recovery_used','mfa_disable_request','mfa_disabled','password_changed','mfa_enabled','payment_failed','account_restored','account_suspended','nonpayment_deletion'));
-- +goose StatementEnd
