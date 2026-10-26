-- filename: 20261027101817572_managed_postgres_cap_events.sql
-- migration-compat: expand
--
-- ADR-20261025094915 decisions 7 and 8: the compute cap sweep records each
-- threshold an account's pooled managed PostgreSQL compute reaches in a UTC
-- month (80% of the allowance, the allowance, 80% of the ceiling with
-- overage on, and suspension at the ceiling), once per subject, month and
-- threshold. The usage view reads suspended_at from it, and the warning
-- email (template managed_postgres_cap) is sent for it. apid is the only
-- writer; rows go with the account or organization.
--
-- The mail outbox CHECKs that list templates are widened online to admit
-- managed_postgres_cap, as in 20261025214605728: the wider CHECK is added
-- NOT VALID under a temporary name and validated, the old one dropped, the
-- same CHECK added back under the old name and validated, and the temporary
-- one dropped. Each step is its own transaction and re-runnable; the
-- previous release never writes the new template.
-- +goose NO TRANSACTION
-- +goose Up
SET lock_timeout = '5s';

CREATE TABLE IF NOT EXISTS managed_postgres_cap_events (
    subject_id uuid NOT NULL REFERENCES resource_namespaces(id) ON DELETE CASCADE,
    period_start timestamptz NOT NULL
        CHECK (period_start = (date_trunc('month', period_start AT TIME ZONE 'UTC') AT TIME ZONE 'UTC')),
    threshold text NOT NULL CHECK (threshold IN ('warning', 'allowance', 'ceiling_warning', 'suspended')),
    reached_at timestamptz NOT NULL CHECK (isfinite(reached_at) AND reached_at >= period_start),
    used_unit_seconds bigint NOT NULL CHECK (used_unit_seconds >= 0),
    allowance_unit_seconds bigint NOT NULL CHECK (allowance_unit_seconds > 0),
    ceiling_unit_seconds bigint NOT NULL CHECK (ceiling_unit_seconds >= allowance_unit_seconds),
    overage_enabled boolean NOT NULL,
    mail_intent_id uuid,
    PRIMARY KEY (subject_id, period_start, threshold)
);
COMMENT ON TABLE managed_postgres_cap_events IS 'Compute cap thresholds a managed PostgreSQL usage subject reached in a UTC month; written only by apid (ADR-20261025094915).';

-- +goose StatementBegin
DO $cap_mail$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'platform_mail_producer_outbox'::regclass AND conname = 'platform_mail_producer_outbox_template_check_v2') THEN
    ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT platform_mail_producer_outbox_template_check_v2
    CHECK (template IN ('account_deletion_pending','email_verification','password_reset','magic_link','mfa_recovery_used','mfa_disable_request','mfa_disabled','password_changed','mfa_enabled','payment_failed','account_restored','account_suspended','nonpayment_deletion','quota_warning','subscription_ended','deployment_failed','managed_postgres_cap')) NOT VALID;
  END IF;
END
$cap_mail$;
-- +goose StatementEnd

ALTER TABLE platform_mail_producer_outbox VALIDATE CONSTRAINT platform_mail_producer_outbox_template_check_v2;

ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT IF EXISTS platform_mail_producer_outbox_template_check;

-- +goose StatementBegin
DO $cap_mail$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'platform_mail_producer_outbox'::regclass AND conname = 'platform_mail_producer_outbox_template_check') THEN
    ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT platform_mail_producer_outbox_template_check
    CHECK (template IN ('account_deletion_pending','email_verification','password_reset','magic_link','mfa_recovery_used','mfa_disable_request','mfa_disabled','password_changed','mfa_enabled','payment_failed','account_restored','account_suspended','nonpayment_deletion','quota_warning','subscription_ended','deployment_failed','managed_postgres_cap')) NOT VALID;
  END IF;
END
$cap_mail$;
-- +goose StatementEnd

ALTER TABLE platform_mail_producer_outbox VALIDATE CONSTRAINT platform_mail_producer_outbox_template_check;

ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT IF EXISTS platform_mail_producer_outbox_template_check_v2;

-- +goose StatementBegin
DO $cap_mail$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'platform_mail_producer_outbox'::regclass AND conname = 'platform_mail_producer_outbox_verification_check_v2') THEN
    ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT platform_mail_producer_outbox_verification_check_v2
    CHECK (
        (template='account_deletion_pending' AND deletion_requested_at IS NOT NULL AND restore_until IS NOT NULL
            AND verification_token_hash IS NULL AND octet_length(sealed_verification)=0 AND verification_key_id='' AND verification_origin='')
        OR (template IN ('mfa_recovery_used','mfa_disabled','password_changed','mfa_enabled','payment_failed','account_restored','account_suspended','nonpayment_deletion','quota_warning','subscription_ended','deployment_failed','managed_postgres_cap') AND deletion_requested_at IS NULL AND restore_until IS NULL
            AND verification_token_hash IS NULL AND octet_length(sealed_verification)=0 AND verification_key_id='' AND verification_origin='')
        OR (template IN ('email_verification','password_reset','magic_link','mfa_disable_request') AND deletion_requested_at IS NULL AND restore_until IS NULL
            AND ((state IN ('queued','claimed') AND verification_token_hash IS NOT NULL AND octet_length(verification_token_hash)=32
                AND octet_length(sealed_verification) BETWEEN 64 AND 4096 AND verification_key_id ~ '^[0-9a-f]{64}$'
                AND length(verification_origin) BETWEEN 9 AND 2048 AND verification_origin ~ '^https://[^/?#[:space:]]+$')
            OR (state NOT IN ('queued','claimed') AND verification_token_hash IS NULL AND octet_length(sealed_verification)=0
                AND verification_key_id='' AND verification_origin='')))
    ) NOT VALID;
  END IF;
END
$cap_mail$;
-- +goose StatementEnd

ALTER TABLE platform_mail_producer_outbox VALIDATE CONSTRAINT platform_mail_producer_outbox_verification_check_v2;

ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT IF EXISTS platform_mail_producer_outbox_verification_check;

-- +goose StatementBegin
DO $cap_mail$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'platform_mail_producer_outbox'::regclass AND conname = 'platform_mail_producer_outbox_verification_check') THEN
    ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT platform_mail_producer_outbox_verification_check
    CHECK (
        (template='account_deletion_pending' AND deletion_requested_at IS NOT NULL AND restore_until IS NOT NULL
            AND verification_token_hash IS NULL AND octet_length(sealed_verification)=0 AND verification_key_id='' AND verification_origin='')
        OR (template IN ('mfa_recovery_used','mfa_disabled','password_changed','mfa_enabled','payment_failed','account_restored','account_suspended','nonpayment_deletion','quota_warning','subscription_ended','deployment_failed','managed_postgres_cap') AND deletion_requested_at IS NULL AND restore_until IS NULL
            AND verification_token_hash IS NULL AND octet_length(sealed_verification)=0 AND verification_key_id='' AND verification_origin='')
        OR (template IN ('email_verification','password_reset','magic_link','mfa_disable_request') AND deletion_requested_at IS NULL AND restore_until IS NULL
            AND ((state IN ('queued','claimed') AND verification_token_hash IS NOT NULL AND octet_length(verification_token_hash)=32
                AND octet_length(sealed_verification) BETWEEN 64 AND 4096 AND verification_key_id ~ '^[0-9a-f]{64}$'
                AND length(verification_origin) BETWEEN 9 AND 2048 AND verification_origin ~ '^https://[^/?#[:space:]]+$')
            OR (state NOT IN ('queued','claimed') AND verification_token_hash IS NULL AND octet_length(sealed_verification)=0
                AND verification_key_id='' AND verification_origin='')))
    ) NOT VALID;
  END IF;
END
$cap_mail$;
-- +goose StatementEnd

ALTER TABLE platform_mail_producer_outbox VALIDATE CONSTRAINT platform_mail_producer_outbox_verification_check;

ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT IF EXISTS platform_mail_producer_outbox_verification_check_v2;

-- +goose StatementBegin
DO $cap_mail$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'platform_mail_producer_outbox'::regclass AND conname = 'platform_mail_producer_outbox_security_alert_check_v2') THEN
    ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT platform_mail_producer_outbox_security_alert_check_v2
    CHECK (
        (template NOT IN ('mfa_recovery_used','mfa_disabled','password_changed','mfa_enabled','payment_failed','account_restored','account_suspended','nonpayment_deletion','quota_warning','subscription_ended','deployment_failed','managed_postgres_cap') AND octet_length(sealed_security_alert)=0 AND security_alert_key_id='')
        OR (template IN ('mfa_recovery_used','mfa_disabled','password_changed','mfa_enabled','payment_failed','account_restored','account_suspended','nonpayment_deletion','quota_warning','subscription_ended','deployment_failed','managed_postgres_cap') AND
            ((state IN ('queued','claimed') AND octet_length(sealed_security_alert) BETWEEN 64 AND 4096 AND security_alert_key_id ~ '^[0-9a-f]{64}$')
            OR (state NOT IN ('queued','claimed') AND octet_length(sealed_security_alert)=0 AND security_alert_key_id='')))
    ) NOT VALID;
  END IF;
END
$cap_mail$;
-- +goose StatementEnd

ALTER TABLE platform_mail_producer_outbox VALIDATE CONSTRAINT platform_mail_producer_outbox_security_alert_check_v2;

ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT IF EXISTS platform_mail_producer_outbox_security_alert_check;

-- +goose StatementBegin
DO $cap_mail$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'platform_mail_producer_outbox'::regclass AND conname = 'platform_mail_producer_outbox_security_alert_check') THEN
    ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT platform_mail_producer_outbox_security_alert_check
    CHECK (
        (template NOT IN ('mfa_recovery_used','mfa_disabled','password_changed','mfa_enabled','payment_failed','account_restored','account_suspended','nonpayment_deletion','quota_warning','subscription_ended','deployment_failed','managed_postgres_cap') AND octet_length(sealed_security_alert)=0 AND security_alert_key_id='')
        OR (template IN ('mfa_recovery_used','mfa_disabled','password_changed','mfa_enabled','payment_failed','account_restored','account_suspended','nonpayment_deletion','quota_warning','subscription_ended','deployment_failed','managed_postgres_cap') AND
            ((state IN ('queued','claimed') AND octet_length(sealed_security_alert) BETWEEN 64 AND 4096 AND security_alert_key_id ~ '^[0-9a-f]{64}$')
            OR (state NOT IN ('queued','claimed') AND octet_length(sealed_security_alert)=0 AND security_alert_key_id='')))
    ) NOT VALID;
  END IF;
END
$cap_mail$;
-- +goose StatementEnd

ALTER TABLE platform_mail_producer_outbox VALIDATE CONSTRAINT platform_mail_producer_outbox_security_alert_check;

ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT IF EXISTS platform_mail_producer_outbox_security_alert_check_v2;

-- +goose StatementBegin
DO $cap_mail$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'platform_mail_outbox_reconciliations'::regclass AND conname = 'platform_mail_outbox_reconciliations_template_check_v2') THEN
    ALTER TABLE platform_mail_outbox_reconciliations ADD CONSTRAINT platform_mail_outbox_reconciliations_template_check_v2
    CHECK (template IN ('account_deletion_pending','email_verification','password_reset','magic_link','mfa_recovery_used','mfa_disable_request','mfa_disabled','password_changed','mfa_enabled','payment_failed','account_restored','account_suspended','nonpayment_deletion','quota_warning','subscription_ended','deployment_failed','managed_postgres_cap')) NOT VALID;
  END IF;
END
$cap_mail$;
-- +goose StatementEnd

ALTER TABLE platform_mail_outbox_reconciliations VALIDATE CONSTRAINT platform_mail_outbox_reconciliations_template_check_v2;

ALTER TABLE platform_mail_outbox_reconciliations DROP CONSTRAINT IF EXISTS platform_mail_outbox_reconciliations_template_check;

-- +goose StatementBegin
DO $cap_mail$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'platform_mail_outbox_reconciliations'::regclass AND conname = 'platform_mail_outbox_reconciliations_template_check') THEN
    ALTER TABLE platform_mail_outbox_reconciliations ADD CONSTRAINT platform_mail_outbox_reconciliations_template_check
    CHECK (template IN ('account_deletion_pending','email_verification','password_reset','magic_link','mfa_recovery_used','mfa_disable_request','mfa_disabled','password_changed','mfa_enabled','payment_failed','account_restored','account_suspended','nonpayment_deletion','quota_warning','subscription_ended','deployment_failed','managed_postgres_cap')) NOT VALID;
  END IF;
END
$cap_mail$;
-- +goose StatementEnd

ALTER TABLE platform_mail_outbox_reconciliations VALIDATE CONSTRAINT platform_mail_outbox_reconciliations_template_check;

ALTER TABLE platform_mail_outbox_reconciliations DROP CONSTRAINT IF EXISTS platform_mail_outbox_reconciliations_template_check_v2;

RESET lock_timeout;

-- +goose Down
SET lock_timeout = '5s';

DELETE FROM platform_mail_producer_outbox WHERE template = 'managed_postgres_cap';
DELETE FROM platform_mail_outbox_reconciliations WHERE template = 'managed_postgres_cap';

-- +goose StatementBegin
DO $cap_mail$
BEGIN
  ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT IF EXISTS platform_mail_producer_outbox_template_check_v2;
  ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT IF EXISTS platform_mail_producer_outbox_template_check;
  ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT platform_mail_producer_outbox_template_check
    CHECK (template IN ('account_deletion_pending','email_verification','password_reset','magic_link','mfa_recovery_used','mfa_disable_request','mfa_disabled','password_changed','mfa_enabled','payment_failed','account_restored','account_suspended','nonpayment_deletion','quota_warning','subscription_ended','deployment_failed'));
END
$cap_mail$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $cap_mail$
BEGIN
  ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT IF EXISTS platform_mail_producer_outbox_verification_check_v2;
  ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT IF EXISTS platform_mail_producer_outbox_verification_check;
  ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT platform_mail_producer_outbox_verification_check
    CHECK (
        (template='account_deletion_pending' AND deletion_requested_at IS NOT NULL AND restore_until IS NOT NULL
            AND verification_token_hash IS NULL AND octet_length(sealed_verification)=0 AND verification_key_id='' AND verification_origin='')
        OR (template IN ('mfa_recovery_used','mfa_disabled','password_changed','mfa_enabled','payment_failed','account_restored','account_suspended','nonpayment_deletion','quota_warning','subscription_ended','deployment_failed') AND deletion_requested_at IS NULL AND restore_until IS NULL
            AND verification_token_hash IS NULL AND octet_length(sealed_verification)=0 AND verification_key_id='' AND verification_origin='')
        OR (template IN ('email_verification','password_reset','magic_link','mfa_disable_request') AND deletion_requested_at IS NULL AND restore_until IS NULL
            AND ((state IN ('queued','claimed') AND verification_token_hash IS NOT NULL AND octet_length(verification_token_hash)=32
                AND octet_length(sealed_verification) BETWEEN 64 AND 4096 AND verification_key_id ~ '^[0-9a-f]{64}$'
                AND length(verification_origin) BETWEEN 9 AND 2048 AND verification_origin ~ '^https://[^/?#[:space:]]+$')
            OR (state NOT IN ('queued','claimed') AND verification_token_hash IS NULL AND octet_length(sealed_verification)=0
                AND verification_key_id='' AND verification_origin='')))
    );
END
$cap_mail$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $cap_mail$
BEGIN
  ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT IF EXISTS platform_mail_producer_outbox_security_alert_check_v2;
  ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT IF EXISTS platform_mail_producer_outbox_security_alert_check;
  ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT platform_mail_producer_outbox_security_alert_check
    CHECK (
        (template NOT IN ('mfa_recovery_used','mfa_disabled','password_changed','mfa_enabled','payment_failed','account_restored','account_suspended','nonpayment_deletion','quota_warning','subscription_ended','deployment_failed') AND octet_length(sealed_security_alert)=0 AND security_alert_key_id='')
        OR (template IN ('mfa_recovery_used','mfa_disabled','password_changed','mfa_enabled','payment_failed','account_restored','account_suspended','nonpayment_deletion','quota_warning','subscription_ended','deployment_failed') AND
            ((state IN ('queued','claimed') AND octet_length(sealed_security_alert) BETWEEN 64 AND 4096 AND security_alert_key_id ~ '^[0-9a-f]{64}$')
            OR (state NOT IN ('queued','claimed') AND octet_length(sealed_security_alert)=0 AND security_alert_key_id='')))
    );
END
$cap_mail$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $cap_mail$
BEGIN
  ALTER TABLE platform_mail_outbox_reconciliations DROP CONSTRAINT IF EXISTS platform_mail_outbox_reconciliations_template_check_v2;
  ALTER TABLE platform_mail_outbox_reconciliations DROP CONSTRAINT IF EXISTS platform_mail_outbox_reconciliations_template_check;
  ALTER TABLE platform_mail_outbox_reconciliations ADD CONSTRAINT platform_mail_outbox_reconciliations_template_check
    CHECK (template IN ('account_deletion_pending','email_verification','password_reset','magic_link','mfa_recovery_used','mfa_disable_request','mfa_disabled','password_changed','mfa_enabled','payment_failed','account_restored','account_suspended','nonpayment_deletion','quota_warning','subscription_ended','deployment_failed'));
END
$cap_mail$;
-- +goose StatementEnd

DROP TABLE IF EXISTS managed_postgres_cap_events;

RESET lock_timeout;
