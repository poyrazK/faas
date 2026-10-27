-- filename: 20261027141052602_platform_mail_agent_run_delivered_outbox.sql
-- migration-compat: expand

-- +goose Up
-- +goose StatementBegin
-- Replacing the template CHECKs locks the two mail outbox tables briefly; each
-- replacement is added NOT VALID and 20261027141127468 validates it, so the
-- full check runs without the exclusive lock.
SET LOCAL lock_timeout = '5s';
-- ADR-20261027090010 §8: an agent run may mail a short summary to its account's own
-- address. apid's producer outbox captures agentd's email delivery records
-- into the sealed agent_run_delivered template, the way it captures
-- deployment failures; it reads agent_deliveries and never writes them.
ALTER TABLE platform_mail_producer_outbox ADD COLUMN IF NOT EXISTS agent_delivery_id uuid;
DO $do$
DECLARE
    target record;
    definition text;
    widened text;
BEGIN
    FOR target IN SELECT * FROM (VALUES
        ('platform_mail_producer_outbox', 'platform_mail_producer_outbox_template_check', 1),
        ('platform_mail_producer_outbox', 'platform_mail_producer_outbox_verification_check', 1),
        ('platform_mail_producer_outbox', 'platform_mail_producer_outbox_security_alert_check', 2),
        ('platform_mail_outbox_reconciliations', 'platform_mail_outbox_reconciliations_template_check', 1)) AS t(rel, name, lists)
    LOOP
        SELECT pg_get_constraintdef(oid) INTO STRICT definition FROM pg_constraint
            WHERE conrelid = target.rel::regclass AND conname = target.name;
        CONTINUE WHEN position('agent_run_delivered' IN definition) > 0;
        IF (length(definition) - length(replace(definition, '''deployment_failed''::text]', ''))) / length('''deployment_failed''::text]') <> target.lists THEN
            RAISE EXCEPTION '% has an unexpected shape: %', target.name, definition;
        END IF;
        widened := replace(definition, '''deployment_failed''::text]', '''deployment_failed''::text, ''agent_run_delivered''::text]');
        EXECUTE format('ALTER TABLE %I DROP CONSTRAINT %I', target.rel, target.name);
        EXECUTE format('ALTER TABLE %I ADD CONSTRAINT %I %s NOT VALID', target.rel, target.name, widened);
    END LOOP;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'platform_mail_producer_outbox'::regclass
        AND conname = 'platform_mail_producer_outbox_agent_delivery_check') THEN
        ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT platform_mail_producer_outbox_agent_delivery_check
            CHECK ((template = 'agent_run_delivered') = (agent_delivery_id IS NOT NULL)) NOT VALID;
    END IF;
END;
$do$;

-- The first enabled capture records where it starts, so enabling the
-- template never mails runs that finished before it.
CREATE TABLE IF NOT EXISTS platform_mail_agent_run_capture (
    namespace   text NOT NULL CHECK (namespace ~ '^[a-z][a-z0-9_-]{0,63}$'),
    producer_id text NOT NULL CHECK (producer_id ~ '^[a-z][a-z0-9_-]{0,31}$'),
    started_at  timestamptz NOT NULL DEFAULT clock_timestamp() CHECK (isfinite(started_at)),
    recovery_id uuid NOT NULL,
    PRIMARY KEY (namespace, producer_id)
);
-- One receipt per captured delivery: agentd reads it to end the delivery.
-- No address, summary or result text is copied here.
CREATE TABLE IF NOT EXISTS platform_mail_agent_run_observations (
    delivery_id uuid PRIMARY KEY REFERENCES agent_deliveries (id) ON DELETE CASCADE,
    account_id  uuid NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    disposition text NOT NULL CHECK (disposition IN ('queued', 'unavailable', 'expired', 'stale')),
    intent_id   uuid UNIQUE REFERENCES platform_mail_producer_outbox (id) ON DELETE SET NULL,
    observed_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK (isfinite(observed_at)),
    CHECK (disposition = 'queued' OR intent_id IS NULL)
);
CREATE INDEX IF NOT EXISTS platform_mail_agent_run_observations_account_idx ON platform_mail_agent_run_observations (account_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
DO $$ BEGIN
    IF EXISTS(SELECT 1 FROM platform_mail_producer_outbox WHERE template='agent_run_delivered')
        OR EXISTS(SELECT 1 FROM platform_mail_outbox_reconciliations WHERE template='agent_run_delivered')
        OR EXISTS(SELECT 1 FROM platform_mail_agent_run_observations) THEN
        RAISE EXCEPTION 'cannot discard agent run summary intent, receipt or reconciliation history';
    END IF;
END $$;
DROP TABLE IF EXISTS platform_mail_agent_run_observations;
DROP TABLE IF EXISTS platform_mail_agent_run_capture;
ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT IF EXISTS platform_mail_producer_outbox_agent_delivery_check;
DO $do$
DECLARE
    target record;
    definition text;
BEGIN
    FOR target IN SELECT * FROM (VALUES
        ('platform_mail_producer_outbox', 'platform_mail_producer_outbox_template_check'),
        ('platform_mail_producer_outbox', 'platform_mail_producer_outbox_verification_check'),
        ('platform_mail_producer_outbox', 'platform_mail_producer_outbox_security_alert_check'),
        ('platform_mail_outbox_reconciliations', 'platform_mail_outbox_reconciliations_template_check')) AS t(rel, name)
    LOOP
        SELECT pg_get_constraintdef(oid) INTO STRICT definition FROM pg_constraint
            WHERE conrelid = target.rel::regclass AND conname = target.name;
        CONTINUE WHEN position('agent_run_delivered' IN definition) = 0;
        EXECUTE format('ALTER TABLE %I DROP CONSTRAINT %I', target.rel, target.name);
        EXECUTE format('ALTER TABLE %I ADD CONSTRAINT %I %s', target.rel, target.name,
            replace(definition, ', ''agent_run_delivered''::text', ''));
    END LOOP;
END;
$do$;
ALTER TABLE platform_mail_producer_outbox DROP COLUMN IF EXISTS agent_delivery_id;
-- +goose StatementEnd
