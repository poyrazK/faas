-- filename: 20261107145315064_automations_become_stored_workflows.sql
-- ADR-20261107190000: automations become stored workflows, with no aliases.
-- Renames the feature's 11 tables (and the constraints and indexes named
-- after them), the live tables' automation_version column, the functions
-- whose bodies name those tables, and the stored values that drive behavior:
-- webhook event kinds (outbox rows, their CHECK and app webhook filters),
-- the pause payload key, agent run triggers, workflow webhook ignore
-- reasons, the two alert presets and the capability's platform flag rows. CI ("ci_automation_*") and hosting ("hosting_automation_*")
-- tables are a different meaning of the word and are not touched.
-- One helper does both directions so Down is the exact inverse of Up.

-- +goose Up
-- +goose StatementBegin
CREATE FUNCTION pg_temp.rename_automation_vocabulary(forward boolean) RETURNS void
LANGUAGE plpgsql AS $fn$
DECLARE
    pairs text[] := ARRAY[
        'workflow_automation_definitions', 'stored_workflows',
        'workflow_automation_revisions', 'stored_workflow_revisions',
        'workflow_automation_failure_guards', 'stored_workflow_failure_guards',
        'workflow_automation_failure_history', 'stored_workflow_failure_history',
        'workflow_automation_failure_policies', 'stored_workflow_failure_policies',
        'workflow_automation_live_baseline_promotions', 'stored_workflow_live_baseline_promotions',
        'workflow_automation_live_baseline_retirements', 'stored_workflow_live_baseline_retirements',
        'workflow_automation_live_suites', 'stored_workflow_live_suites',
        'workflow_automation_live_verifications', 'stored_workflow_live_verifications',
        'workflow_automation_publish_policies', 'stored_workflow_publish_policies',
        'workflow_automation_publish_receipts', 'stored_workflow_publish_receipts'];
    i int;
    old_table text; new_table text;
    old_prefix text := CASE WHEN forward THEN 'workflow_automation_' ELSE 'stored_workflow_' END;
    new_prefix text := CASE WHEN forward THEN 'stored_workflow_' ELSE 'workflow_automation_' END;
    old_event text := CASE WHEN forward THEN 'automation.' ELSE 'stored_workflow.' END;
    new_event text := CASE WHEN forward THEN 'stored_workflow.' ELSE 'automation.' END;
    old_flag text := CASE WHEN forward THEN 'automations' ELSE 'stored_workflows' END;
    new_flag text := CASE WHEN forward THEN 'stored_workflows' ELSE 'automations' END;
    old_name_key text := CASE WHEN forward THEN 'automation_name' ELSE 'stored_workflow_name' END;
    new_name_key text := CASE WHEN forward THEN 'stored_workflow_name' ELSE 'automation_name' END;
    old_reason text := CASE WHEN forward THEN 'automation_' ELSE 'stored_workflow_' END;
    new_reason text := CASE WHEN forward THEN 'stored_workflow_' ELSE 'automation_' END;
    old_trigger text := CASE WHEN forward THEN 'automation' ELSE 'stored_workflow' END;
    new_trigger text := CASE WHEN forward THEN 'stored_workflow' ELSE 'automation' END;
    old_column text := CASE WHEN forward THEN 'automation_version' ELSE 'workflow_version' END;
    new_column text := CASE WHEN forward THEN 'workflow_version' ELSE 'automation_version' END;
    r record;
    def text;
    renamed text;
BEGIN
    -- Tables, then the constraints and indexes that carry the old table name.
    FOR i IN 1 .. array_length(pairs, 1) / 2 LOOP
        old_table := CASE WHEN forward THEN pairs[2*i-1] ELSE pairs[2*i] END;
        new_table := CASE WHEN forward THEN pairs[2*i] ELSE pairs[2*i-1] END;
        EXECUTE format('ALTER TABLE %I RENAME TO %I', old_table, new_table);
        FOR r IN SELECT conname FROM pg_constraint
                 WHERE conrelid = new_table::regclass
                   AND (strpos(conname, old_table) = 1 OR strpos(conname, old_prefix) = 1) LOOP
            renamed := CASE WHEN strpos(r.conname, old_table) = 1
                THEN new_table || substr(r.conname, length(old_table) + 1)
                ELSE new_prefix || substr(r.conname, length(old_prefix) + 1) END;
            EXECUTE format('ALTER TABLE %I RENAME CONSTRAINT %I TO %I', new_table, r.conname, left(renamed, 63));
        END LOOP;
        FOR r IN SELECT indexname FROM pg_indexes
                 WHERE schemaname = 'public' AND tablename = new_table
                   AND (strpos(indexname, old_table) = 1 OR strpos(indexname, old_prefix) = 1) LOOP
            renamed := CASE WHEN strpos(r.indexname, old_table) = 1
                THEN new_table || substr(r.indexname, length(old_table) + 1)
                ELSE new_prefix || substr(r.indexname, length(old_prefix) + 1) END;
            EXECUTE format('ALTER INDEX %I RENAME TO %I', r.indexname, left(renamed, 63));
        END LOOP;
    END LOOP;

    FOREACH new_table IN ARRAY ARRAY[
        CASE WHEN forward THEN 'stored_workflow_live_suites' ELSE 'workflow_automation_live_suites' END,
        CASE WHEN forward THEN 'stored_workflow_live_verifications' ELSE 'workflow_automation_live_verifications' END] LOOP
        EXECUTE format('ALTER TABLE %I RENAME COLUMN %I TO %I', new_table, old_column, new_column);
    END LOOP;

    -- The failure index on workflow_runs and the version sequence carry the
    -- old name without belonging to a renamed table.
    IF forward THEN
        ALTER INDEX workflow_automation_failure_finished_idx RENAME TO stored_workflow_failure_finished_idx;
        ALTER SEQUENCE automation_definition_versions RENAME TO stored_workflow_versions;
    ELSE
        ALTER INDEX stored_workflow_failure_finished_idx RENAME TO workflow_automation_failure_finished_idx;
        ALTER SEQUENCE stored_workflow_versions RENAME TO automation_definition_versions;
    END IF;

    -- Function bodies resolve table names when they run.
    FOR r IN SELECT p.oid FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
             WHERE n.nspname = 'public'
               AND strpos(p.prosrc, CASE WHEN forward THEN 'workflow_automation_' ELSE 'stored_workflow' END) > 0 LOOP
        def := pg_get_functiondef(r.oid);
        FOR i IN 1 .. array_length(pairs, 1) / 2 LOOP
            def := CASE WHEN forward THEN replace(def, pairs[2*i-1], pairs[2*i])
                        ELSE replace(def, pairs[2*i], pairs[2*i-1]) END;
        END LOOP;
        EXECUTE def;
    END LOOP;

    -- Webhook event kinds: outbox rows, the outbox CHECK and app filters.
    SELECT pg_get_constraintdef(oid) INTO def FROM pg_constraint
     WHERE conrelid = 'app_webhook_event_outbox'::regclass AND conname = 'app_webhook_event_outbox_event_chk';
    IF def IS NULL THEN RAISE EXCEPTION 'missing app_webhook_event_outbox_event_chk'; END IF;
    ALTER TABLE app_webhook_event_outbox DROP CONSTRAINT app_webhook_event_outbox_event_chk;
    UPDATE app_webhook_event_outbox SET event = new_event || substr(event, length(old_event) + 1)
     WHERE strpos(event, old_event) = 1;
    -- The pause payload names the stored workflow under the new key.
    UPDATE app_webhook_event_outbox
       SET payload = (payload - old_name_key) || jsonb_build_object(new_name_key, payload -> old_name_key)
     WHERE event = new_event || 'paused' AND payload ? old_name_key;
    EXECUTE 'ALTER TABLE app_webhook_event_outbox ADD CONSTRAINT app_webhook_event_outbox_event_chk '
        || replace(def, '''' || old_event, '''' || new_event);
    UPDATE app_webhooks SET event_filter = (
        SELECT array_agg(CASE WHEN strpos(e, old_event) = 1 THEN new_event || substr(e, length(old_event) + 1) ELSE e END ORDER BY ord)
          FROM unnest(event_filter) WITH ORDINALITY AS f(e, ord))
     WHERE EXISTS (SELECT 1 FROM unnest(event_filter) AS f(e) WHERE strpos(e, old_event) = 1);

    -- Agent run triggers.
    SELECT pg_get_constraintdef(oid) INTO def FROM pg_constraint
     WHERE conrelid = 'agent_runs'::regclass AND conname = 'agent_runs_trigger_check';
    IF def IS NULL THEN RAISE EXCEPTION 'missing agent_runs_trigger_check'; END IF;
    ALTER TABLE agent_runs DROP CONSTRAINT agent_runs_trigger_check;
    UPDATE agent_runs SET trigger = new_trigger WHERE trigger = old_trigger;
    EXECUTE 'ALTER TABLE agent_runs ADD CONSTRAINT agent_runs_trigger_check '
        || replace(def, '''' || old_trigger || '''', '''' || new_trigger || '''');

    -- Workflow webhook ignore reasons.
    SELECT pg_get_constraintdef(oid) INTO def FROM pg_constraint
     WHERE conrelid = 'workflow_webhook_receipts'::regclass AND conname = 'workflow_webhook_receipts_ignored_reason_check';
    IF def IS NULL THEN RAISE EXCEPTION 'missing workflow_webhook_receipts_ignored_reason_check'; END IF;
    ALTER TABLE workflow_webhook_receipts DROP CONSTRAINT workflow_webhook_receipts_ignored_reason_check;
    UPDATE workflow_webhook_receipts SET ignored_reason = new_reason || substr(ignored_reason, length(old_reason) + 1)
     WHERE strpos(ignored_reason, old_reason) = 1;
    EXECUTE 'ALTER TABLE workflow_webhook_receipts ADD CONSTRAINT workflow_webhook_receipts_ignored_reason_check '
        || replace(def, '''' || old_reason, '''' || new_reason);

    -- The capability's platform flag and its opt-ins follow the capability id.
    UPDATE runtime_config_entries SET config_key = replace(config_key, old_flag, new_flag)
     WHERE config_key IN ('platform_flag.' || old_flag, 'platform_flag_opt_ins.' || old_flag);

    -- Alert presets are addressed by name (`gregale alert preset enable <name>`).
    UPDATE alert_presets SET name = new_reason || substr(name, length(old_reason) + 1),
        display_name = CASE WHEN forward THEN replace(display_name, 'Automation', 'Stored workflow')
                            ELSE replace(display_name, 'Stored workflow', 'Automation') END,
        description = CASE WHEN forward THEN replace(description, 'automation', 'stored workflow')
                           ELSE replace(description, 'stored workflow', 'automation') END,
        updated_at = now()
     WHERE name IN (old_reason || 'backlog', old_reason || 'failures');
END
$fn$;
SELECT pg_temp.rename_automation_vocabulary(true);
DROP FUNCTION pg_temp.rename_automation_vocabulary(boolean);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE FUNCTION pg_temp.rename_automation_vocabulary(forward boolean) RETURNS void
LANGUAGE plpgsql AS $fn$
DECLARE
    pairs text[] := ARRAY[
        'workflow_automation_definitions', 'stored_workflows',
        'workflow_automation_revisions', 'stored_workflow_revisions',
        'workflow_automation_failure_guards', 'stored_workflow_failure_guards',
        'workflow_automation_failure_history', 'stored_workflow_failure_history',
        'workflow_automation_failure_policies', 'stored_workflow_failure_policies',
        'workflow_automation_live_baseline_promotions', 'stored_workflow_live_baseline_promotions',
        'workflow_automation_live_baseline_retirements', 'stored_workflow_live_baseline_retirements',
        'workflow_automation_live_suites', 'stored_workflow_live_suites',
        'workflow_automation_live_verifications', 'stored_workflow_live_verifications',
        'workflow_automation_publish_policies', 'stored_workflow_publish_policies',
        'workflow_automation_publish_receipts', 'stored_workflow_publish_receipts'];
    i int;
    old_table text; new_table text;
    old_prefix text := CASE WHEN forward THEN 'workflow_automation_' ELSE 'stored_workflow_' END;
    new_prefix text := CASE WHEN forward THEN 'stored_workflow_' ELSE 'workflow_automation_' END;
    old_event text := CASE WHEN forward THEN 'automation.' ELSE 'stored_workflow.' END;
    new_event text := CASE WHEN forward THEN 'stored_workflow.' ELSE 'automation.' END;
    old_flag text := CASE WHEN forward THEN 'automations' ELSE 'stored_workflows' END;
    new_flag text := CASE WHEN forward THEN 'stored_workflows' ELSE 'automations' END;
    old_name_key text := CASE WHEN forward THEN 'automation_name' ELSE 'stored_workflow_name' END;
    new_name_key text := CASE WHEN forward THEN 'stored_workflow_name' ELSE 'automation_name' END;
    old_reason text := CASE WHEN forward THEN 'automation_' ELSE 'stored_workflow_' END;
    new_reason text := CASE WHEN forward THEN 'stored_workflow_' ELSE 'automation_' END;
    old_trigger text := CASE WHEN forward THEN 'automation' ELSE 'stored_workflow' END;
    new_trigger text := CASE WHEN forward THEN 'stored_workflow' ELSE 'automation' END;
    old_column text := CASE WHEN forward THEN 'automation_version' ELSE 'workflow_version' END;
    new_column text := CASE WHEN forward THEN 'workflow_version' ELSE 'automation_version' END;
    r record;
    def text;
    renamed text;
BEGIN
    -- Tables, then the constraints and indexes that carry the old table name.
    FOR i IN 1 .. array_length(pairs, 1) / 2 LOOP
        old_table := CASE WHEN forward THEN pairs[2*i-1] ELSE pairs[2*i] END;
        new_table := CASE WHEN forward THEN pairs[2*i] ELSE pairs[2*i-1] END;
        EXECUTE format('ALTER TABLE %I RENAME TO %I', old_table, new_table);
        FOR r IN SELECT conname FROM pg_constraint
                 WHERE conrelid = new_table::regclass
                   AND (strpos(conname, old_table) = 1 OR strpos(conname, old_prefix) = 1) LOOP
            renamed := CASE WHEN strpos(r.conname, old_table) = 1
                THEN new_table || substr(r.conname, length(old_table) + 1)
                ELSE new_prefix || substr(r.conname, length(old_prefix) + 1) END;
            EXECUTE format('ALTER TABLE %I RENAME CONSTRAINT %I TO %I', new_table, r.conname, left(renamed, 63));
        END LOOP;
        FOR r IN SELECT indexname FROM pg_indexes
                 WHERE schemaname = 'public' AND tablename = new_table
                   AND (strpos(indexname, old_table) = 1 OR strpos(indexname, old_prefix) = 1) LOOP
            renamed := CASE WHEN strpos(r.indexname, old_table) = 1
                THEN new_table || substr(r.indexname, length(old_table) + 1)
                ELSE new_prefix || substr(r.indexname, length(old_prefix) + 1) END;
            EXECUTE format('ALTER INDEX %I RENAME TO %I', r.indexname, left(renamed, 63));
        END LOOP;
    END LOOP;

    FOREACH new_table IN ARRAY ARRAY[
        CASE WHEN forward THEN 'stored_workflow_live_suites' ELSE 'workflow_automation_live_suites' END,
        CASE WHEN forward THEN 'stored_workflow_live_verifications' ELSE 'workflow_automation_live_verifications' END] LOOP
        EXECUTE format('ALTER TABLE %I RENAME COLUMN %I TO %I', new_table, old_column, new_column);
    END LOOP;

    -- The failure index on workflow_runs and the version sequence carry the
    -- old name without belonging to a renamed table.
    IF forward THEN
        ALTER INDEX workflow_automation_failure_finished_idx RENAME TO stored_workflow_failure_finished_idx;
        ALTER SEQUENCE automation_definition_versions RENAME TO stored_workflow_versions;
    ELSE
        ALTER INDEX stored_workflow_failure_finished_idx RENAME TO workflow_automation_failure_finished_idx;
        ALTER SEQUENCE stored_workflow_versions RENAME TO automation_definition_versions;
    END IF;

    -- Function bodies resolve table names when they run.
    FOR r IN SELECT p.oid FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
             WHERE n.nspname = 'public'
               AND strpos(p.prosrc, CASE WHEN forward THEN 'workflow_automation_' ELSE 'stored_workflow' END) > 0 LOOP
        def := pg_get_functiondef(r.oid);
        FOR i IN 1 .. array_length(pairs, 1) / 2 LOOP
            def := CASE WHEN forward THEN replace(def, pairs[2*i-1], pairs[2*i])
                        ELSE replace(def, pairs[2*i], pairs[2*i-1]) END;
        END LOOP;
        EXECUTE def;
    END LOOP;

    -- Webhook event kinds: outbox rows, the outbox CHECK and app filters.
    SELECT pg_get_constraintdef(oid) INTO def FROM pg_constraint
     WHERE conrelid = 'app_webhook_event_outbox'::regclass AND conname = 'app_webhook_event_outbox_event_chk';
    IF def IS NULL THEN RAISE EXCEPTION 'missing app_webhook_event_outbox_event_chk'; END IF;
    ALTER TABLE app_webhook_event_outbox DROP CONSTRAINT app_webhook_event_outbox_event_chk;
    UPDATE app_webhook_event_outbox SET event = new_event || substr(event, length(old_event) + 1)
     WHERE strpos(event, old_event) = 1;
    -- The pause payload names the stored workflow under the new key.
    UPDATE app_webhook_event_outbox
       SET payload = (payload - old_name_key) || jsonb_build_object(new_name_key, payload -> old_name_key)
     WHERE event = new_event || 'paused' AND payload ? old_name_key;
    EXECUTE 'ALTER TABLE app_webhook_event_outbox ADD CONSTRAINT app_webhook_event_outbox_event_chk '
        || replace(def, '''' || old_event, '''' || new_event);
    UPDATE app_webhooks SET event_filter = (
        SELECT array_agg(CASE WHEN strpos(e, old_event) = 1 THEN new_event || substr(e, length(old_event) + 1) ELSE e END ORDER BY ord)
          FROM unnest(event_filter) WITH ORDINALITY AS f(e, ord))
     WHERE EXISTS (SELECT 1 FROM unnest(event_filter) AS f(e) WHERE strpos(e, old_event) = 1);

    -- Agent run triggers.
    SELECT pg_get_constraintdef(oid) INTO def FROM pg_constraint
     WHERE conrelid = 'agent_runs'::regclass AND conname = 'agent_runs_trigger_check';
    IF def IS NULL THEN RAISE EXCEPTION 'missing agent_runs_trigger_check'; END IF;
    ALTER TABLE agent_runs DROP CONSTRAINT agent_runs_trigger_check;
    UPDATE agent_runs SET trigger = new_trigger WHERE trigger = old_trigger;
    EXECUTE 'ALTER TABLE agent_runs ADD CONSTRAINT agent_runs_trigger_check '
        || replace(def, '''' || old_trigger || '''', '''' || new_trigger || '''');

    -- Workflow webhook ignore reasons.
    SELECT pg_get_constraintdef(oid) INTO def FROM pg_constraint
     WHERE conrelid = 'workflow_webhook_receipts'::regclass AND conname = 'workflow_webhook_receipts_ignored_reason_check';
    IF def IS NULL THEN RAISE EXCEPTION 'missing workflow_webhook_receipts_ignored_reason_check'; END IF;
    ALTER TABLE workflow_webhook_receipts DROP CONSTRAINT workflow_webhook_receipts_ignored_reason_check;
    UPDATE workflow_webhook_receipts SET ignored_reason = new_reason || substr(ignored_reason, length(old_reason) + 1)
     WHERE strpos(ignored_reason, old_reason) = 1;
    EXECUTE 'ALTER TABLE workflow_webhook_receipts ADD CONSTRAINT workflow_webhook_receipts_ignored_reason_check '
        || replace(def, '''' || old_reason, '''' || new_reason);

    -- The capability's platform flag and its opt-ins follow the capability id.
    UPDATE runtime_config_entries SET config_key = replace(config_key, old_flag, new_flag)
     WHERE config_key IN ('platform_flag.' || old_flag, 'platform_flag_opt_ins.' || old_flag);

    -- Alert presets are addressed by name (`gregale alert preset enable <name>`).
    UPDATE alert_presets SET name = new_reason || substr(name, length(old_reason) + 1),
        display_name = CASE WHEN forward THEN replace(display_name, 'Automation', 'Stored workflow')
                            ELSE replace(display_name, 'Stored workflow', 'Automation') END,
        description = CASE WHEN forward THEN replace(description, 'automation', 'stored workflow')
                           ELSE replace(description, 'stored workflow', 'automation') END,
        updated_at = now()
     WHERE name IN (old_reason || 'backlog', old_reason || 'failures');
END
$fn$;
SELECT pg_temp.rename_automation_vocabulary(false);
DROP FUNCTION pg_temp.rename_automation_vocabulary(boolean);
-- +goose StatementEnd
