-- filename: 20261017155842001_platform_mail_deployment_failed_outbox.sql

-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT IF EXISTS platform_mail_producer_outbox_template_check;
ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT platform_mail_producer_outbox_template_check
    CHECK (template IN ('account_deletion_pending','email_verification','password_reset','magic_link','mfa_recovery_used','mfa_disable_request','mfa_disabled','password_changed','mfa_enabled','payment_failed','account_restored','account_suspended','nonpayment_deletion','quota_warning','subscription_ended','deployment_failed'));
ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT IF EXISTS platform_mail_producer_outbox_verification_check;
ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT platform_mail_producer_outbox_verification_check CHECK (
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
ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT IF EXISTS platform_mail_producer_outbox_security_alert_check;
ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT platform_mail_producer_outbox_security_alert_check CHECK (
    (template NOT IN ('mfa_recovery_used','mfa_disabled','password_changed','mfa_enabled','payment_failed','account_restored','account_suspended','nonpayment_deletion','quota_warning','subscription_ended','deployment_failed') AND octet_length(sealed_security_alert)=0 AND security_alert_key_id='')
    OR (template IN ('mfa_recovery_used','mfa_disabled','password_changed','mfa_enabled','payment_failed','account_restored','account_suspended','nonpayment_deletion','quota_warning','subscription_ended','deployment_failed') AND
        ((state IN ('queued','claimed') AND octet_length(sealed_security_alert) BETWEEN 64 AND 4096 AND security_alert_key_id ~ '^[0-9a-f]{64}$')
        OR (state NOT IN ('queued','claimed') AND octet_length(sealed_security_alert)=0 AND security_alert_key_id='')))
);
ALTER TABLE platform_mail_outbox_reconciliations DROP CONSTRAINT IF EXISTS platform_mail_outbox_reconciliations_template_check;
ALTER TABLE platform_mail_outbox_reconciliations ADD CONSTRAINT platform_mail_outbox_reconciliations_template_check
    CHECK (template IN ('account_deletion_pending','email_verification','password_reset','magic_link','mfa_recovery_used','mfa_disable_request','mfa_disabled','password_changed','mfa_enabled','payment_failed','account_restored','account_suspended','nonpayment_deletion','quota_warning','subscription_ended','deployment_failed'));
-- Passive lifecycle metadata has no sender, credentials or queue dependency.
ALTER TABLE accounts ADD COLUMN IF NOT EXISTS deployment_mail_revision bigint NOT NULL DEFAULT 1 CHECK(deployment_mail_revision>0);
CREATE OR REPLACE FUNCTION advance_account_deployment_mail_revision() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.email IS DISTINCT FROM OLD.email OR NEW.status IS DISTINCT FROM OLD.status
        OR NEW.deletion_requested_at IS DISTINCT FROM OLD.deletion_requested_at
        OR NEW.deployment_mail_revision IS DISTINCT FROM OLD.deployment_mail_revision THEN
        NEW.deployment_mail_revision := OLD.deployment_mail_revision+1;
    ELSE NEW.deployment_mail_revision := OLD.deployment_mail_revision;
    END IF;
    RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS accounts_deployment_mail_revision_guard ON accounts;
CREATE TRIGGER accounts_deployment_mail_revision_guard
    BEFORE UPDATE OF email,status,deletion_requested_at,deployment_mail_revision ON accounts
    FOR EACH ROW EXECUTE FUNCTION advance_account_deployment_mail_revision();
ALTER TABLE apps ADD COLUMN IF NOT EXISTS deployment_mail_revision bigint NOT NULL DEFAULT 1 CHECK(deployment_mail_revision>0);
CREATE OR REPLACE FUNCTION advance_app_deployment_mail_revision() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.account_id IS DISTINCT FROM OLD.account_id OR NEW.org_id IS DISTINCT FROM OLD.org_id
        OR NEW.project_id IS DISTINCT FROM OLD.project_id OR NEW.slug IS DISTINCT FROM OLD.slug
        OR NEW.status IS DISTINCT FROM OLD.status OR NEW.deployment_mail_revision IS DISTINCT FROM OLD.deployment_mail_revision THEN
        NEW.deployment_mail_revision := OLD.deployment_mail_revision+1;
    ELSE NEW.deployment_mail_revision := OLD.deployment_mail_revision;
    END IF;
    RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS apps_deployment_mail_revision_guard ON apps;
CREATE TRIGGER apps_deployment_mail_revision_guard
    BEFORE UPDATE OF account_id,org_id,project_id,slug,status,deployment_mail_revision ON apps
    FOR EACH ROW EXECUTE FUNCTION advance_app_deployment_mail_revision();
-- A separate row avoids deployment -> app/account lock inversions. Mail
-- readers hold this fence but never lock deployment rows. Scope is authoritative.
CREATE TABLE IF NOT EXISTS platform_mail_deployment_fences (
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    scope text NOT NULL CHECK(scope ~ '^[a-z0-9]([a-z0-9-]{1,38})[a-z0-9]$'),
    revision bigint NOT NULL DEFAULT 1 CHECK(revision>0),
    success_revision bigint NOT NULL DEFAULT 0 CHECK(success_revision>=0),
    PRIMARY KEY(app_id,scope)
);
ALTER TABLE deployments ADD COLUMN IF NOT EXISTS mail_failure_revision bigint NOT NULL DEFAULT 1 CHECK(mail_failure_revision>0);
ALTER TABLE deployments ADD COLUMN IF NOT EXISTS mail_failed_at timestamptz CHECK(mail_failed_at IS NULL OR isfinite(mail_failed_at));
ALTER TABLE deployments ADD COLUMN IF NOT EXISTS mail_failure_account_id uuid;
ALTER TABLE deployments ADD COLUMN IF NOT EXISTS mail_failure_account_revision bigint CHECK(mail_failure_account_revision IS NULL OR mail_failure_account_revision>0);
ALTER TABLE deployments ADD COLUMN IF NOT EXISTS mail_failure_app_revision bigint CHECK(mail_failure_app_revision IS NULL OR mail_failure_app_revision>0);
ALTER TABLE deployments ADD COLUMN IF NOT EXISTS mail_failure_success_revision bigint CHECK(mail_failure_success_revision IS NULL OR mail_failure_success_revision>=0);
CREATE OR REPLACE FUNCTION stamp_deployment_mail_failure() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE success bigint; recipient uuid; app_revision bigint; account_revision bigint; became_live boolean; became_failed boolean;
BEGIN
    IF TG_OP='DELETE' THEN
        UPDATE platform_mail_deployment_fences SET revision=revision+1 WHERE app_id=OLD.app_id AND scope=OLD.scope;
        RETURN OLD;
    END IF;
    -- Acquire the ordinary parent FK key-share before the scope fence on
    -- insert/reassignment. Capturers use NO KEY UPDATE for the cooldown,
    -- which is compatible with this lock and avoids a parent/fence cycle.
    IF TG_OP='INSERT' THEN
        PERFORM 1 FROM apps WHERE id=NEW.app_id FOR KEY SHARE;
    ELSIF NEW.app_id<>OLD.app_id THEN
        PERFORM 1 FROM apps WHERE id=NEW.app_id FOR KEY SHARE;
    END IF;
    IF TG_OP='UPDATE' THEN
        NEW.mail_failed_at := OLD.mail_failed_at;
        NEW.mail_failure_account_id := OLD.mail_failure_account_id;
        NEW.mail_failure_account_revision := OLD.mail_failure_account_revision;
        NEW.mail_failure_app_revision := OLD.mail_failure_app_revision;
        NEW.mail_failure_success_revision := OLD.mail_failure_success_revision;
        IF NEW.status IS NOT DISTINCT FROM OLD.status AND NEW.app_id=OLD.app_id AND NEW.scope=OLD.scope
            AND NEW.deployed_by_user_id IS NOT DISTINCT FROM OLD.deployed_by_user_id
            AND NEW.deleted_at IS NOT DISTINCT FROM OLD.deleted_at
            AND NEW.mail_failure_revision=OLD.mail_failure_revision THEN RETURN NEW;
        END IF;
        NEW.mail_failure_revision := OLD.mail_failure_revision+1;
        -- Ordered fences for the exceptional app/scope reassignment path.
        PERFORM 1 FROM platform_mail_deployment_fences
            WHERE (app_id=OLD.app_id AND scope=OLD.scope) OR (app_id=NEW.app_id AND scope=NEW.scope)
            ORDER BY app_id,scope FOR UPDATE;
        IF NEW.app_id<>OLD.app_id OR NEW.scope<>OLD.scope THEN
            UPDATE platform_mail_deployment_fences SET revision=revision+1 WHERE app_id=OLD.app_id AND scope=OLD.scope;
        END IF;
        became_live := NEW.status='live' AND (OLD.status<>'live' OR NEW.app_id<>OLD.app_id OR NEW.scope<>OLD.scope);
        became_failed := NEW.status='failed' AND OLD.status<>'failed';
    ELSE
        NEW.mail_failure_revision := 1;
        NEW.mail_failed_at := NULL; NEW.mail_failure_account_id := NULL;
        NEW.mail_failure_account_revision := NULL; NEW.mail_failure_app_revision := NULL; NEW.mail_failure_success_revision := NULL;
        became_live := NEW.status='live'; became_failed := NEW.status='failed';
    END IF;
    INSERT INTO platform_mail_deployment_fences(app_id,scope,success_revision)
        VALUES(NEW.app_id,NEW.scope,CASE WHEN became_live THEN 1 ELSE 0 END)
        ON CONFLICT(app_id,scope) DO UPDATE SET revision=platform_mail_deployment_fences.revision+1,
            success_revision=platform_mail_deployment_fences.success_revision+CASE WHEN became_live THEN 1 ELSE 0 END
        RETURNING success_revision INTO success;
    IF became_failed THEN
        SELECT coalesce(NEW.deployed_by_user_id,a.account_id),a.deployment_mail_revision
            INTO recipient,app_revision FROM apps a WHERE a.id=NEW.app_id;
        SELECT deployment_mail_revision INTO account_revision FROM accounts WHERE id=recipient;
        NEW.mail_failed_at := clock_timestamp(); NEW.mail_failure_account_id := recipient;
        NEW.mail_failure_account_revision := account_revision; NEW.mail_failure_app_revision := app_revision;
        NEW.mail_failure_success_revision := success;
    END IF;
    RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS deployments_mail_failure_guard ON deployments;
CREATE TRIGGER deployments_mail_failure_guard
    BEFORE INSERT OR DELETE OR UPDATE OF status,app_id,scope,deployed_by_user_id,deleted_at,mail_failure_revision,mail_failed_at,
        mail_failure_account_id,mail_failure_account_revision,mail_failure_app_revision,mail_failure_success_revision ON deployments
    FOR EACH ROW EXECUTE FUNCTION stamp_deployment_mail_failure();
-- The pending-failure index is built concurrently by the following index migration.
CREATE TABLE IF NOT EXISTS platform_mail_deployment_capture (
    namespace text NOT NULL CHECK(namespace ~ '^[a-z][a-z0-9_-]{0,63}$'),
    producer_id text NOT NULL CHECK(producer_id ~ '^[a-z][a-z0-9_-]{0,31}$'),
    started_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK(isfinite(started_at)),
    recovery_id uuid NOT NULL,
    PRIMARY KEY(namespace,producer_id)
);
ALTER TABLE platform_mail_producer_outbox ADD COLUMN IF NOT EXISTS deployment_failure_id uuid;
ALTER TABLE platform_mail_producer_outbox ADD COLUMN IF NOT EXISTS deployment_failure_app_id uuid;
ALTER TABLE platform_mail_producer_outbox ADD COLUMN IF NOT EXISTS deployment_failure_scope text;
ALTER TABLE platform_mail_producer_outbox ADD COLUMN IF NOT EXISTS deployment_failure_revision bigint;
ALTER TABLE platform_mail_producer_outbox ADD COLUMN IF NOT EXISTS deployment_failure_account_revision bigint;
ALTER TABLE platform_mail_producer_outbox ADD COLUMN IF NOT EXISTS deployment_failure_app_revision bigint;
ALTER TABLE platform_mail_producer_outbox ADD COLUMN IF NOT EXISTS deployment_failure_success_revision bigint;
ALTER TABLE platform_mail_producer_outbox ADD COLUMN IF NOT EXISTS deployment_failure_at timestamptz;
ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT IF EXISTS platform_mail_producer_outbox_deployment_failure_check;
ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT platform_mail_producer_outbox_deployment_failure_check CHECK (
    (template='deployment_failed' AND deployment_failure_id IS NOT NULL AND deployment_failure_app_id IS NOT NULL
        AND deployment_failure_scope IS NOT NULL AND deployment_failure_scope ~ '^[a-z0-9]([a-z0-9-]{1,38})[a-z0-9]$'
        AND deployment_failure_revision IS NOT NULL AND deployment_failure_revision>0
        AND deployment_failure_account_revision IS NOT NULL AND deployment_failure_account_revision>0
        AND deployment_failure_app_revision IS NOT NULL AND deployment_failure_app_revision>0
        AND deployment_failure_success_revision IS NOT NULL AND deployment_failure_success_revision>=0
        AND deployment_failure_at IS NOT NULL AND isfinite(deployment_failure_at))
    OR (template<>'deployment_failed' AND deployment_failure_id IS NULL AND deployment_failure_app_id IS NULL
        AND deployment_failure_scope IS NULL AND deployment_failure_revision IS NULL AND deployment_failure_account_revision IS NULL
        AND deployment_failure_app_revision IS NULL AND deployment_failure_success_revision IS NULL AND deployment_failure_at IS NULL)
);
CREATE UNIQUE INDEX IF NOT EXISTS platform_mail_producer_outbox_deployment_failure_idx
    ON platform_mail_producer_outbox(deployment_failure_id,deployment_failure_revision) WHERE template='deployment_failed';
CREATE INDEX IF NOT EXISTS platform_mail_producer_outbox_deployment_failure_keys_idx
    ON platform_mail_producer_outbox(namespace,producer_id,security_alert_key_id) WHERE template='deployment_failed' AND state IN ('queued','claimed');
-- Durable skip receipts ensure cooldown-suppressed failures never reappear.
-- Account erasure cascades; no address, error text or logs are copied here.
CREATE TABLE IF NOT EXISTS platform_mail_deployment_observations (
    deployment_id uuid NOT NULL,
    failure_revision bigint NOT NULL CHECK(failure_revision>0),
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    disposition text NOT NULL CHECK(disposition IN ('queued','cooldown','stale','unavailable','expired')),
    intent_id uuid UNIQUE REFERENCES platform_mail_producer_outbox(id) ON DELETE SET NULL,
    observed_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK(isfinite(observed_at)),
    PRIMARY KEY(deployment_id,failure_revision),
    CHECK(disposition='queued' OR intent_id IS NULL)
);
CREATE INDEX IF NOT EXISTS platform_mail_deployment_observations_account_idx ON platform_mail_deployment_observations(account_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
LOCK TABLE platform_mail_producer_outbox,platform_mail_outbox_reconciliations,platform_mail_deployment_observations,platform_mail_deployment_capture,deployments,apps,accounts IN ACCESS EXCLUSIVE MODE;
DO $$ BEGIN
    IF EXISTS(SELECT 1 FROM platform_mail_producer_outbox WHERE template='deployment_failed')
        OR EXISTS(SELECT 1 FROM platform_mail_outbox_reconciliations WHERE template='deployment_failed')
        OR EXISTS(SELECT 1 FROM platform_mail_deployment_observations) THEN
        RAISE EXCEPTION 'cannot discard deployment-failure intent, observation or reconciliation history';
    END IF;
END $$;
DROP TABLE platform_mail_deployment_observations;
DROP TABLE platform_mail_deployment_capture;
DROP INDEX platform_mail_producer_outbox_deployment_failure_idx;
DROP INDEX platform_mail_producer_outbox_deployment_failure_keys_idx;
ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT platform_mail_producer_outbox_deployment_failure_check;
ALTER TABLE platform_mail_producer_outbox DROP COLUMN deployment_failure_id,DROP COLUMN deployment_failure_app_id,
    DROP COLUMN deployment_failure_scope,DROP COLUMN deployment_failure_revision,DROP COLUMN deployment_failure_account_revision,
    DROP COLUMN deployment_failure_app_revision,DROP COLUMN deployment_failure_success_revision,DROP COLUMN deployment_failure_at;
DROP TRIGGER deployments_mail_failure_guard ON deployments;
DROP FUNCTION stamp_deployment_mail_failure();
DROP INDEX deployments_mail_failures_pending_idx;
ALTER TABLE deployments DROP COLUMN mail_failure_revision,DROP COLUMN mail_failed_at,DROP COLUMN mail_failure_account_id,
    DROP COLUMN mail_failure_account_revision,DROP COLUMN mail_failure_app_revision,DROP COLUMN mail_failure_success_revision;
DROP TABLE platform_mail_deployment_fences;
DROP TRIGGER apps_deployment_mail_revision_guard ON apps;
DROP FUNCTION advance_app_deployment_mail_revision();
ALTER TABLE apps DROP COLUMN deployment_mail_revision;
DROP TRIGGER accounts_deployment_mail_revision_guard ON accounts;
DROP FUNCTION advance_account_deployment_mail_revision();
ALTER TABLE accounts DROP COLUMN deployment_mail_revision;
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
-- +goose StatementEnd
