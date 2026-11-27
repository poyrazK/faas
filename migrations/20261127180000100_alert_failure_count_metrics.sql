-- filename: 20261127180000100_alert_failure_count_metrics.sql
-- migration-compat: expand
-- Alerts for failures only support could see (pkg/apid/attention):
-- boot_failures counts the app's failed VM boots (wake.boot_failed, reason
-- vmm_boot_failed, not mirror-mode); job_failures counts the account's job
-- runs that finished failed or dead_letter (a rule on any app counts every
-- job, like account_spend_eur). Both are webhook-only
-- (alert_rules_failure_count_action_chk): a node fault also fails boots and
-- neither count is tied to one deployment. Each gets a preset. The metric
-- CHECKs are widened online as in 20261112122644583_ai_budget_alert_metric;
-- every step commits on its own and is guarded so a rerun converges.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';
-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'alert_rules'::regclass AND conname = 'alert_rules_failure_count_action_chk') THEN
  ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_failure_count_action_chk CHECK ((metric NOT IN ('boot_failures', 'job_failures')) OR (action = 'webhook')) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE alert_rules VALIDATE CONSTRAINT alert_rules_failure_count_action_chk;
-- +goose StatementBegin
DO $$ DECLARE base text;
BEGIN
 IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'alert_rules'::regclass AND conname = 'alert_rules_metric_failure_count_chk') THEN RETURN; END IF;
 SELECT pg_get_constraintdef(oid) INTO base FROM pg_constraint WHERE conrelid = 'alert_rules'::regclass AND conname = 'alert_rules_metric_chk';
 IF base IS NULL OR position('boot_failures' in base) > 0 THEN RETURN; END IF;
 EXECUTE format('ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_metric_failure_count_chk CHECK ((%s) OR metric IN (%L, %L)) NOT VALID',
   regexp_replace(base, '^CHECK \((.*)\)$', '\1'), 'boot_failures', 'job_failures');
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'alert_rules'::regclass AND conname = 'alert_rules_metric_failure_count_chk') THEN
  ALTER TABLE alert_rules VALIDATE CONSTRAINT alert_rules_metric_failure_count_chk;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'alert_rules'::regclass AND conname = 'alert_rules_metric_failure_count_chk') THEN
  ALTER TABLE alert_rules DROP CONSTRAINT IF EXISTS alert_rules_metric_chk;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $$ DECLARE widened text;
BEGIN
 IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'alert_rules'::regclass AND conname = 'alert_rules_metric_chk') THEN RETURN; END IF;
 SELECT pg_get_constraintdef(oid) INTO widened FROM pg_constraint WHERE conrelid = 'alert_rules'::regclass AND conname = 'alert_rules_metric_failure_count_chk';
 EXECUTE 'ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_metric_chk ' || widened || ' NOT VALID';
END $$;
-- +goose StatementEnd
ALTER TABLE alert_rules VALIDATE CONSTRAINT alert_rules_metric_chk;
ALTER TABLE alert_rules DROP CONSTRAINT IF EXISTS alert_rules_metric_failure_count_chk;
-- +goose StatementBegin
DO $$ DECLARE base text;
BEGIN
 IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'alert_presets'::regclass AND conname = 'alert_presets_metric_failure_count_chk') THEN RETURN; END IF;
 SELECT pg_get_constraintdef(oid) INTO base FROM pg_constraint WHERE conrelid = 'alert_presets'::regclass AND conname = 'alert_presets_metric_chk';
 IF base IS NULL OR position('boot_failures' in base) > 0 THEN RETURN; END IF;
 EXECUTE format('ALTER TABLE alert_presets ADD CONSTRAINT alert_presets_metric_failure_count_chk CHECK ((%s) OR metric IN (%L, %L)) NOT VALID',
   regexp_replace(base, '^CHECK \((.*)\)$', '\1'), 'boot_failures', 'job_failures');
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'alert_presets'::regclass AND conname = 'alert_presets_metric_failure_count_chk') THEN
  ALTER TABLE alert_presets VALIDATE CONSTRAINT alert_presets_metric_failure_count_chk;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'alert_presets'::regclass AND conname = 'alert_presets_metric_failure_count_chk') THEN
  ALTER TABLE alert_presets DROP CONSTRAINT IF EXISTS alert_presets_metric_chk;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $$ DECLARE widened text;
BEGIN
 IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'alert_presets'::regclass AND conname = 'alert_presets_metric_chk') THEN RETURN; END IF;
 SELECT pg_get_constraintdef(oid) INTO widened FROM pg_constraint WHERE conrelid = 'alert_presets'::regclass AND conname = 'alert_presets_metric_failure_count_chk';
 EXECUTE 'ALTER TABLE alert_presets ADD CONSTRAINT alert_presets_metric_chk ' || widened || ' NOT VALID';
END $$;
-- +goose StatementEnd
ALTER TABLE alert_presets VALIDATE CONSTRAINT alert_presets_metric_chk;
ALTER TABLE alert_presets DROP CONSTRAINT IF EXISTS alert_presets_metric_failure_count_chk;
INSERT INTO alert_presets(name, display_name, description, category, metric, comparison, threshold, window_spec, default_cooldown_minutes, minimum_plan)
SELECT 'boot_failing', 'App failing to start',
       'The app failed to boot 3 or more times in 15 minutes.',
       'availability', 'boot_failures', 'gte', 3, '15m', 60, 'hobby'
WHERE NOT EXISTS (SELECT 1 FROM alert_presets WHERE name = 'boot_failing');
INSERT INTO alert_presets(name, display_name, description, category, metric, comparison, threshold, window_spec, default_cooldown_minutes, minimum_plan)
SELECT 'job_failures', 'Job runs failing',
       'A job run in this account finished failed or dead-lettered in the last hour.',
       'reliability', 'job_failures', 'gt', 0, '1h', 60, 'hobby'
WHERE NOT EXISTS (SELECT 1 FROM alert_presets WHERE name = 'job_failures');
RESET lock_timeout;

-- +goose Down
SET lock_timeout = '5s';
-- +goose StatementBegin
DO $$ DECLARE rules_def text; presets_def text;
 clause constant text := ' OR (metric = ANY (ARRAY[''boot_failures''::text, ''job_failures''::text]))';
BEGIN
 IF EXISTS (SELECT 1 FROM alert_rules WHERE metric IN ('boot_failures', 'job_failures')) THEN
  RAISE EXCEPTION 'delete boot_failures and job_failures alert rules before rollback';
 END IF;
 DELETE FROM alert_presets WHERE name IN ('boot_failing', 'job_failures');
 ALTER TABLE alert_rules DROP CONSTRAINT IF EXISTS alert_rules_failure_count_action_chk;
 SELECT pg_get_constraintdef(oid) INTO rules_def FROM pg_constraint WHERE conrelid = 'alert_rules'::regclass AND conname = 'alert_rules_metric_chk';
 IF position(clause in rules_def) > 0 THEN
  ALTER TABLE alert_rules DROP CONSTRAINT alert_rules_metric_chk;
  EXECUTE 'ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_metric_chk ' || replace(rules_def, clause, '');
 END IF;
 SELECT pg_get_constraintdef(oid) INTO presets_def FROM pg_constraint WHERE conrelid = 'alert_presets'::regclass AND conname = 'alert_presets_metric_chk';
 IF position(clause in presets_def) > 0 THEN
  ALTER TABLE alert_presets DROP CONSTRAINT alert_presets_metric_chk;
  EXECUTE 'ALTER TABLE alert_presets ADD CONSTRAINT alert_presets_metric_chk ' || replace(presets_def, clause, '');
 END IF;
END $$;
-- +goose StatementEnd
RESET lock_timeout;
