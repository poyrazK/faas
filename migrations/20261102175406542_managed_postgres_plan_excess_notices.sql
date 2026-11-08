-- filename: 20261102175406542_managed_postgres_plan_excess_notices.sql
-- migration-compat: expand

-- ADR-20261102125208 step 3: the compute cap sweep records a notice for each
-- managed PostgreSQL database it suspends because the plan no longer
-- includes it, and drops the notice once the database is back inside the
-- plan, so a later suspension is noticed again. The email (template
-- managed_postgres_plan_excess) links its intent to the row, so a notice is
-- mailed at most once. apid is the only writer; rows go with the database.
--
-- The mail outbox CHECKs that list templates are widened online to admit
-- managed_postgres_plan_excess, as 20261101085949320 does for
-- managed_postgres_restore_slot: each list holding managed_postgres_cap gets
-- the template at its end, added NOT VALID, then validated in a later
-- transaction. Every step is re-runnable. The previous release never writes
-- the new template.
-- +goose NO TRANSACTION
-- +goose Up
SET lock_timeout = '5s';

CREATE TABLE IF NOT EXISTS managed_postgres_plan_excess_notices (
    database_id uuid PRIMARY KEY REFERENCES managed_postgres_databases(id) ON DELETE CASCADE,
    reason text NOT NULL CHECK (reason IN ('database_limit', 'service_class')),
    recorded_at timestamptz NOT NULL CHECK (isfinite(recorded_at)),
    mail_intent_id uuid
);
COMMENT ON TABLE managed_postgres_plan_excess_notices IS 'Notices for managed PostgreSQL databases suspended past their plan; written only by apid (ADR-20261102125208).';

-- +goose StatementBegin
DO $plan_excess_mail$
DECLARE
    target record;
    constraint_name name;
    definition text;
BEGIN
    FOR target IN SELECT * FROM (VALUES
        ('platform_mail_producer_outbox', '^platform_mail_producer_outbox_template_check$', 1),
        ('platform_mail_producer_outbox', '^platform_mail_producer_outbox_verification(_v2)?_check$', 1),
        ('platform_mail_producer_outbox', '^platform_mail_producer_outbox_security_alert_check$', 2),
        ('platform_mail_outbox_reconciliations', '^platform_mail_outbox_reconciliations_template_check$', 1)) AS t(rel, pattern, lists)
    LOOP
        SELECT conname, pg_get_constraintdef(oid) INTO STRICT constraint_name, definition FROM pg_constraint
            WHERE conrelid = target.rel::regclass AND conname ~ target.pattern;
        CONTINUE WHEN position('managed_postgres_plan_excess' IN definition) > 0;
        -- The template ends each list that holds managed_postgres_cap, after
        -- any template a later migration added behind it.
        IF (SELECT count(*) FROM regexp_matches(definition, '''managed_postgres_cap''::text(, ''[a-z_]+''::text)*\]', 'g')) <> target.lists THEN
            RAISE EXCEPTION '% has an unexpected shape: %', constraint_name, definition;
        END IF;
        EXECUTE format('ALTER TABLE %I DROP CONSTRAINT %I', target.rel, constraint_name);
        EXECUTE format('ALTER TABLE %I ADD CONSTRAINT %I %s NOT VALID', target.rel, constraint_name,
            regexp_replace(definition, '(''managed_postgres_cap''::text(, ''[a-z_]+''::text)*)\]', '\1, ''managed_postgres_plan_excess''::text]', 'g'));
    END LOOP;
END
$plan_excess_mail$;
-- +goose StatementEnd

-- +goose StatementBegin
-- VALIDATE takes only a SHARE UPDATE EXCLUSIVE lock, so writes continue.
DO $plan_excess_validate$
DECLARE
    target record;
BEGIN
    FOR target IN SELECT t.relname, c.conname FROM pg_constraint c JOIN pg_class t ON t.oid = c.conrelid
        WHERE NOT c.convalidated AND t.relnamespace = current_schema()::regnamespace
          AND t.relname IN ('platform_mail_producer_outbox', 'platform_mail_outbox_reconciliations')
          AND position('managed_postgres_plan_excess' IN pg_get_constraintdef(c.oid)) > 0
    LOOP
        EXECUTE format('ALTER TABLE %I VALIDATE CONSTRAINT %I', target.relname, target.conname);
    END LOOP;
END
$plan_excess_validate$;
-- +goose StatementEnd

RESET lock_timeout;

-- +goose Down
SET lock_timeout = '5s';

-- +goose StatementBegin
DO $plan_excess_mail$
DECLARE
    target record;
    constraint_name name;
    definition text;
BEGIN
    IF EXISTS(SELECT 1 FROM platform_mail_producer_outbox WHERE template = 'managed_postgres_plan_excess')
        OR EXISTS(SELECT 1 FROM platform_mail_outbox_reconciliations WHERE template = 'managed_postgres_plan_excess') THEN
        RAISE EXCEPTION 'cannot discard managed postgres plan excess intent or reconciliation history';
    END IF;
    FOR target IN SELECT * FROM (VALUES
        ('platform_mail_producer_outbox', '^platform_mail_producer_outbox_template_check$'),
        ('platform_mail_producer_outbox', '^platform_mail_producer_outbox_verification(_v2)?_check$'),
        ('platform_mail_producer_outbox', '^platform_mail_producer_outbox_security_alert_check$'),
        ('platform_mail_outbox_reconciliations', '^platform_mail_outbox_reconciliations_template_check$')) AS t(rel, pattern)
    LOOP
        SELECT conname, pg_get_constraintdef(oid) INTO STRICT constraint_name, definition FROM pg_constraint
            WHERE conrelid = target.rel::regclass AND conname ~ target.pattern;
        CONTINUE WHEN position('managed_postgres_plan_excess' IN definition) = 0;
        EXECUTE format('ALTER TABLE %I DROP CONSTRAINT %I', target.rel, constraint_name);
        EXECUTE format('ALTER TABLE %I ADD CONSTRAINT %I %s', target.rel, constraint_name,
            replace(definition, ', ''managed_postgres_plan_excess''::text', ''));
    END LOOP;
END
$plan_excess_mail$;
-- +goose StatementEnd

DROP TABLE IF EXISTS managed_postgres_plan_excess_notices;

RESET lock_timeout;
