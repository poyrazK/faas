-- filename: 20261126120000100_platform_mail_managed_postgres_awake.sql
-- migration-compat: contract
-- migration-lint: allow dynamic-ddl: each DROP is followed in the same transaction by the same-named CHECK with one more template, so the previous release's writes stay admitted; no table, column or name it reads goes away

-- ADR-20261125231222: the weekly awake email (template managed_postgres_awake)
-- marks its managed_postgres_awake_notices row mailed in the transaction that
-- queues it, so a notice is mailed at most once.
--
-- The mail outbox CHECKs that list templates are widened online to admit
-- managed_postgres_awake, as 20261102175406542 does for
-- managed_postgres_plan_excess: each list holding managed_postgres_cap gets
-- the template at its end, added NOT VALID, then validated in a later
-- transaction. Every step is re-runnable. The previous release never writes
-- the new template.
-- +goose NO TRANSACTION
-- +goose Up
SET lock_timeout = '5s';

-- +goose StatementBegin
DO $awake_mail$
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
        CONTINUE WHEN position('managed_postgres_awake' IN definition) > 0;
        -- The template ends each list that holds managed_postgres_cap, after
        -- any template a later migration added behind it.
        IF (SELECT count(*) FROM regexp_matches(definition, '''managed_postgres_cap''::text(, ''[a-z_]+''::text)*\]', 'g')) <> target.lists THEN
            RAISE EXCEPTION '% has an unexpected shape: %', constraint_name, definition;
        END IF;
        EXECUTE format('ALTER TABLE %I DROP CONSTRAINT %I', target.rel, constraint_name);
        EXECUTE format('ALTER TABLE %I ADD CONSTRAINT %I %s NOT VALID', target.rel, constraint_name,
            regexp_replace(definition, '(''managed_postgres_cap''::text(, ''[a-z_]+''::text)*)\]', '\1, ''managed_postgres_awake''::text]', 'g'));
    END LOOP;
END
$awake_mail$;
-- +goose StatementEnd

-- +goose StatementBegin
-- VALIDATE takes only a SHARE UPDATE EXCLUSIVE lock, so writes continue.
DO $awake_validate$
DECLARE
    target record;
BEGIN
    FOR target IN SELECT t.relname, c.conname FROM pg_constraint c JOIN pg_class t ON t.oid = c.conrelid
        WHERE NOT c.convalidated AND t.relnamespace = current_schema()::regnamespace
          AND t.relname IN ('platform_mail_producer_outbox', 'platform_mail_outbox_reconciliations')
          AND position('managed_postgres_awake' IN pg_get_constraintdef(c.oid)) > 0
    LOOP
        EXECUTE format('ALTER TABLE %I VALIDATE CONSTRAINT %I', target.relname, target.conname);
    END LOOP;
END
$awake_validate$;
-- +goose StatementEnd

RESET lock_timeout;

-- +goose Down
SET lock_timeout = '5s';

-- +goose StatementBegin
DO $awake_mail$
DECLARE
    target record;
    constraint_name name;
    definition text;
BEGIN
    IF EXISTS(SELECT 1 FROM platform_mail_producer_outbox WHERE template = 'managed_postgres_awake')
        OR EXISTS(SELECT 1 FROM platform_mail_outbox_reconciliations WHERE template = 'managed_postgres_awake') THEN
        RAISE EXCEPTION 'cannot discard managed postgres awake intent or reconciliation history';
    END IF;
    FOR target IN SELECT * FROM (VALUES
        ('platform_mail_producer_outbox', '^platform_mail_producer_outbox_template_check$'),
        ('platform_mail_producer_outbox', '^platform_mail_producer_outbox_verification(_v2)?_check$'),
        ('platform_mail_producer_outbox', '^platform_mail_producer_outbox_security_alert_check$'),
        ('platform_mail_outbox_reconciliations', '^platform_mail_outbox_reconciliations_template_check$')) AS t(rel, pattern)
    LOOP
        SELECT conname, pg_get_constraintdef(oid) INTO STRICT constraint_name, definition FROM pg_constraint
            WHERE conrelid = target.rel::regclass AND conname ~ target.pattern;
        CONTINUE WHEN position('managed_postgres_awake' IN definition) = 0;
        EXECUTE format('ALTER TABLE %I DROP CONSTRAINT %I', target.rel, constraint_name);
        EXECUTE format('ALTER TABLE %I ADD CONSTRAINT %I %s', target.rel, constraint_name,
            replace(definition, ', ''managed_postgres_awake''::text', ''));
    END LOOP;
END
$awake_mail$;
-- +goose StatementEnd

RESET lock_timeout;
