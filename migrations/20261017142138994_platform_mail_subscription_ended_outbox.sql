-- filename: 20261017142138994_platform_mail_subscription_ended_outbox.sql

-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT IF EXISTS platform_mail_producer_outbox_template_check;
ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT platform_mail_producer_outbox_template_check
    CHECK (template IN ('account_deletion_pending','email_verification','password_reset','magic_link','mfa_recovery_used','mfa_disable_request','mfa_disabled','password_changed','mfa_enabled','payment_failed','account_restored','account_suspended','nonpayment_deletion','quota_warning','subscription_ended'));
ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT IF EXISTS platform_mail_producer_outbox_verification_check;
ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT platform_mail_producer_outbox_verification_check CHECK (
    (template='account_deletion_pending' AND deletion_requested_at IS NOT NULL AND restore_until IS NOT NULL
        AND verification_token_hash IS NULL AND octet_length(sealed_verification)=0 AND verification_key_id='' AND verification_origin='')
    OR (template IN ('mfa_recovery_used','mfa_disabled','password_changed','mfa_enabled','payment_failed','account_restored','account_suspended','nonpayment_deletion','quota_warning','subscription_ended') AND deletion_requested_at IS NULL AND restore_until IS NULL
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
    (template NOT IN ('mfa_recovery_used','mfa_disabled','password_changed','mfa_enabled','payment_failed','account_restored','account_suspended','nonpayment_deletion','quota_warning','subscription_ended') AND octet_length(sealed_security_alert)=0 AND security_alert_key_id='')
    OR (template IN ('mfa_recovery_used','mfa_disabled','password_changed','mfa_enabled','payment_failed','account_restored','account_suspended','nonpayment_deletion','quota_warning','subscription_ended') AND
        ((state IN ('queued','claimed') AND octet_length(sealed_security_alert) BETWEEN 64 AND 4096 AND security_alert_key_id ~ '^[0-9a-f]{64}$')
        OR (state NOT IN ('queued','claimed') AND octet_length(sealed_security_alert)=0 AND security_alert_key_id='')))
);
ALTER TABLE platform_mail_outbox_reconciliations DROP CONSTRAINT IF EXISTS platform_mail_outbox_reconciliations_template_check;
ALTER TABLE platform_mail_outbox_reconciliations ADD CONSTRAINT platform_mail_outbox_reconciliations_template_check
    CHECK (template IN ('account_deletion_pending','email_verification','password_reset','magic_link','mfa_recovery_used','mfa_disable_request','mfa_disabled','password_changed','mfa_enabled','payment_failed','account_restored','account_suspended','nonpayment_deletion','quota_warning','subscription_ended'));
ALTER TABLE accounts ADD COLUMN IF NOT EXISTS subscription_mail_revision bigint NOT NULL DEFAULT 1 CHECK(subscription_mail_revision>0);
CREATE OR REPLACE FUNCTION advance_account_subscription_mail_revision() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.plan IS DISTINCT FROM OLD.plan OR NEW.email IS DISTINCT FROM OLD.email
        OR NEW.status IS DISTINCT FROM OLD.status OR NEW.past_due_at IS DISTINCT FROM OLD.past_due_at
        OR NEW.deletion_requested_at IS DISTINCT FROM OLD.deletion_requested_at
        OR NEW.provider_customer_id IS DISTINCT FROM OLD.provider_customer_id
        OR NEW.stripe_subscription_item IS DISTINCT FROM OLD.stripe_subscription_item
        OR NEW.subscription_mail_revision IS DISTINCT FROM OLD.subscription_mail_revision THEN
        NEW.subscription_mail_revision := OLD.subscription_mail_revision+1;
    ELSE
        NEW.subscription_mail_revision := OLD.subscription_mail_revision;
    END IF;
    RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS accounts_subscription_mail_revision_guard ON accounts;
CREATE TRIGGER accounts_subscription_mail_revision_guard
    BEFORE UPDATE OF plan,email,status,past_due_at,deletion_requested_at,provider_customer_id,stripe_subscription_item,subscription_mail_revision ON accounts
    FOR EACH ROW EXECUTE FUNCTION advance_account_subscription_mail_revision();
-- All platform billing identity writers take the account lock first. This
-- trigger also fences direct identity edits, including delete/insert cycles.
CREATE OR REPLACE FUNCTION advance_billing_subscription_mail_revision() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE old_account uuid; new_account uuid;
BEGIN
    IF TG_OP='UPDATE' AND NEW.account_id=OLD.account_id AND NEW.provider=OLD.provider
        AND NEW.customer_id=OLD.customer_id AND NEW.subscription_id=OLD.subscription_id THEN
        RETURN NULL;
    END IF;
    IF TG_OP<>'INSERT' THEN old_account := OLD.account_id; END IF;
    IF TG_OP<>'DELETE' THEN new_account := NEW.account_id; END IF;
    UPDATE accounts SET subscription_mail_revision=subscription_mail_revision+1
        WHERE id=old_account OR id=new_account;
    RETURN NULL;
END $$;
DROP TRIGGER IF EXISTS billing_identities_subscription_mail_revision_guard ON billing_identities;
CREATE TRIGGER billing_identities_subscription_mail_revision_guard
    AFTER INSERT OR UPDATE OR DELETE ON billing_identities
    FOR EACH ROW EXECUTE FUNCTION advance_billing_subscription_mail_revision();
ALTER TABLE platform_mail_producer_outbox ADD COLUMN IF NOT EXISTS subscription_ended_revision bigint;
ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT IF EXISTS platform_mail_producer_outbox_subscription_ended_check;
ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT platform_mail_producer_outbox_subscription_ended_check CHECK (
    (template='subscription_ended' AND subscription_ended_revision IS NOT NULL AND subscription_ended_revision>0)
    OR (template<>'subscription_ended' AND subscription_ended_revision IS NULL)
);
CREATE UNIQUE INDEX IF NOT EXISTS platform_mail_producer_outbox_subscription_ended_revision_idx
    ON platform_mail_producer_outbox(account_id,subscription_ended_revision) WHERE template='subscription_ended';
CREATE INDEX IF NOT EXISTS platform_mail_producer_outbox_subscription_ended_keys_idx
    ON platform_mail_producer_outbox(namespace,producer_id,security_alert_key_id)
    WHERE template='subscription_ended' AND state IN ('queued','claimed');
-- Permanent provider delivery proof; no raw customer/subscription handle.
CREATE TABLE IF NOT EXISTS billing_subscription_end_receipts (
    provider text NOT NULL CHECK(provider IN ('stripe','paddle','polar')),
    event_id text NOT NULL CHECK(length(event_id) BETWEEN 1 AND 256 AND position(chr(10) in event_id)=0 AND position(chr(13) in event_id)=0),
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    customer_hash bytea NOT NULL CHECK(octet_length(customer_hash)=32),
    subscription_hash bytea NOT NULL CHECK(octet_length(subscription_hash)=32),
    processed_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK(isfinite(processed_at)),
    PRIMARY KEY(provider,event_id)
);
CREATE INDEX IF NOT EXISTS billing_subscription_end_receipts_account_idx ON billing_subscription_end_receipts(account_id);
-- Distinct provider deliveries for an already-ended subscription cannot apply
-- the transition again, including after a subscription/plan cycle.
CREATE TABLE IF NOT EXISTS billing_subscription_ends (
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    provider text NOT NULL CHECK(provider IN ('stripe','paddle','polar')),
    subscription_hash bytea NOT NULL CHECK(octet_length(subscription_hash)=32),
    ended_at timestamptz NOT NULL CHECK(isfinite(ended_at)),
    from_plan text NOT NULL CHECK(from_plan IN ('free','hobby','pro','scale')),
    intent_id uuid UNIQUE REFERENCES platform_mail_producer_outbox(id) ON DELETE SET NULL,
    PRIMARY KEY(account_id,provider,subscription_hash)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
LOCK TABLE platform_mail_producer_outbox,platform_mail_outbox_reconciliations,billing_subscription_end_receipts,billing_subscription_ends,accounts,billing_identities IN ACCESS EXCLUSIVE MODE;
DO $$
BEGIN
    IF EXISTS(SELECT 1 FROM platform_mail_producer_outbox WHERE template='subscription_ended')
        OR EXISTS(SELECT 1 FROM platform_mail_outbox_reconciliations WHERE template='subscription_ended')
        OR EXISTS(SELECT 1 FROM billing_subscription_end_receipts)
        OR EXISTS(SELECT 1 FROM billing_subscription_ends) THEN
        RAISE EXCEPTION 'cannot discard subscription-ended intent, receipt, transition or reconciliation history';
    END IF;
END $$;
DROP TABLE billing_subscription_end_receipts;
DROP TABLE billing_subscription_ends;
DROP INDEX platform_mail_producer_outbox_subscription_ended_revision_idx;
DROP INDEX platform_mail_producer_outbox_subscription_ended_keys_idx;
ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT platform_mail_producer_outbox_subscription_ended_check;
ALTER TABLE platform_mail_producer_outbox DROP COLUMN subscription_ended_revision;
DROP TRIGGER billing_identities_subscription_mail_revision_guard ON billing_identities;
DROP FUNCTION advance_billing_subscription_mail_revision();
DROP TRIGGER accounts_subscription_mail_revision_guard ON accounts;
DROP FUNCTION advance_account_subscription_mail_revision();
ALTER TABLE accounts DROP COLUMN subscription_mail_revision;
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
-- +goose StatementEnd
