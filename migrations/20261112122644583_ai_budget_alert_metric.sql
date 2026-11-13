-- filename: 20261112122644583_ai_budget_alert_metric.sql
-- migration-compat: expand
-- ADR 20261027132936 amendment 2: AI budget threshold crossings reach alert
-- rules and notification channels, not only the outbound.ai_budget.threshold
-- app webhook. ai_budget_alerts counts the crossings (80% and 100% of a
-- budget) of the integrations bound to the rule's app within the window. It
-- is app-scoped and notification-only, with windows up to 24 hours, like the
-- route alert metrics. A preset puts it in the dashboard grid.
--
-- alert_rules_ai_budget_alerts_chk is added NOT VALID and validated first.
-- Each metric CHECK is then replaced online, as in
-- 20261028140007417_route_alert_metrics: a widened copy of its current
-- definition (which keeps metrics other migrations added) is added NOT VALID
-- under a new name and validated, the old one is dropped, the old name is
-- re-added from the copy NOT VALID and validated, and the copy is dropped.
-- Every step commits on its own and is guarded, so writers never wait on a
-- scan and a rerun after a partial failure converges.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';
-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'alert_rules'::regclass AND conname = 'alert_rules_ai_budget_alerts_chk') THEN
  ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_ai_budget_alerts_chk CHECK ((metric <> 'ai_budget_alerts')
    OR ((app_id IS NOT NULL) AND (event_subscription_id IS NULL) AND (action = 'webhook')
      AND (window_spec IN ('5m', '15m', '1h', '6h', '24h')))) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE alert_rules VALIDATE CONSTRAINT alert_rules_ai_budget_alerts_chk;
-- +goose StatementBegin
DO $$ DECLARE base text;
BEGIN
 IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'alert_rules'::regclass AND conname = 'alert_rules_metric_ai_budget_alerts_chk') THEN RETURN; END IF;
 SELECT pg_get_constraintdef(oid) INTO base FROM pg_constraint WHERE conrelid = 'alert_rules'::regclass AND conname = 'alert_rules_metric_chk';
 IF base IS NULL OR position('ai_budget_alerts' in base) > 0 THEN RETURN; END IF;
 EXECUTE format('ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_metric_ai_budget_alerts_chk CHECK ((%s) OR metric IN (%L)) NOT VALID',
   regexp_replace(base, '^CHECK \((.*)\)$', '\1'), 'ai_budget_alerts');
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'alert_rules'::regclass AND conname = 'alert_rules_metric_ai_budget_alerts_chk') THEN
  ALTER TABLE alert_rules VALIDATE CONSTRAINT alert_rules_metric_ai_budget_alerts_chk;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'alert_rules'::regclass AND conname = 'alert_rules_metric_ai_budget_alerts_chk') THEN
  ALTER TABLE alert_rules DROP CONSTRAINT IF EXISTS alert_rules_metric_chk;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $$ DECLARE widened text;
BEGIN
 IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'alert_rules'::regclass AND conname = 'alert_rules_metric_chk') THEN RETURN; END IF;
 SELECT pg_get_constraintdef(oid) INTO widened FROM pg_constraint WHERE conrelid = 'alert_rules'::regclass AND conname = 'alert_rules_metric_ai_budget_alerts_chk';
 EXECUTE 'ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_metric_chk ' || widened || ' NOT VALID';
END $$;
-- +goose StatementEnd
ALTER TABLE alert_rules VALIDATE CONSTRAINT alert_rules_metric_chk;
ALTER TABLE alert_rules DROP CONSTRAINT IF EXISTS alert_rules_metric_ai_budget_alerts_chk;
-- +goose StatementBegin
DO $$ DECLARE base text;
BEGIN
 IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'alert_presets'::regclass AND conname = 'alert_presets_metric_ai_budget_alerts_chk') THEN RETURN; END IF;
 SELECT pg_get_constraintdef(oid) INTO base FROM pg_constraint WHERE conrelid = 'alert_presets'::regclass AND conname = 'alert_presets_metric_chk';
 IF base IS NULL OR position('ai_budget_alerts' in base) > 0 THEN RETURN; END IF;
 EXECUTE format('ALTER TABLE alert_presets ADD CONSTRAINT alert_presets_metric_ai_budget_alerts_chk CHECK ((%s) OR metric IN (%L)) NOT VALID',
   regexp_replace(base, '^CHECK \((.*)\)$', '\1'), 'ai_budget_alerts');
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'alert_presets'::regclass AND conname = 'alert_presets_metric_ai_budget_alerts_chk') THEN
  ALTER TABLE alert_presets VALIDATE CONSTRAINT alert_presets_metric_ai_budget_alerts_chk;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'alert_presets'::regclass AND conname = 'alert_presets_metric_ai_budget_alerts_chk') THEN
  ALTER TABLE alert_presets DROP CONSTRAINT IF EXISTS alert_presets_metric_chk;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $$ DECLARE widened text;
BEGIN
 IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'alert_presets'::regclass AND conname = 'alert_presets_metric_chk') THEN RETURN; END IF;
 SELECT pg_get_constraintdef(oid) INTO widened FROM pg_constraint WHERE conrelid = 'alert_presets'::regclass AND conname = 'alert_presets_metric_ai_budget_alerts_chk';
 EXECUTE 'ALTER TABLE alert_presets ADD CONSTRAINT alert_presets_metric_chk ' || widened || ' NOT VALID';
END $$;
-- +goose StatementEnd
ALTER TABLE alert_presets VALIDATE CONSTRAINT alert_presets_metric_chk;
ALTER TABLE alert_presets DROP CONSTRAINT IF EXISTS alert_presets_metric_ai_budget_alerts_chk;
INSERT INTO alert_presets(name, display_name, description, category, metric, comparison, threshold, window_spec, default_cooldown_minutes, minimum_plan)
SELECT 'ai_budget_alerts', 'AI budget threshold',
       'An AI integration the app uses reached 80% or 100% of a daily or monthly budget.',
       'cost', 'ai_budget_alerts', 'gt', 0, '15m', 60, 'hobby'
WHERE NOT EXISTS (SELECT 1 FROM alert_presets WHERE name = 'ai_budget_alerts');
RESET lock_timeout;

-- +goose Down
SET lock_timeout = '5s';
-- +goose StatementBegin
DO $$ DECLARE rules_def text; presets_def text;
BEGIN
 IF EXISTS (SELECT 1 FROM alert_rules WHERE metric = 'ai_budget_alerts') THEN
  RAISE EXCEPTION 'delete ai_budget_alerts alert rules before rollback';
 END IF;
 DELETE FROM alert_presets WHERE name = 'ai_budget_alerts';
 ALTER TABLE alert_rules DROP CONSTRAINT IF EXISTS alert_rules_ai_budget_alerts_chk;
 SELECT pg_get_constraintdef(oid) INTO rules_def FROM pg_constraint WHERE conrelid = 'alert_rules'::regclass AND conname = 'alert_rules_metric_chk';
 IF position(' OR (metric = ''ai_budget_alerts''::text)' in rules_def) > 0 THEN
  ALTER TABLE alert_rules DROP CONSTRAINT alert_rules_metric_chk;
  EXECUTE 'ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_metric_chk ' || replace(rules_def, ' OR (metric = ''ai_budget_alerts''::text)', '');
 END IF;
 SELECT pg_get_constraintdef(oid) INTO presets_def FROM pg_constraint WHERE conrelid = 'alert_presets'::regclass AND conname = 'alert_presets_metric_chk';
 IF position(' OR (metric = ''ai_budget_alerts''::text)' in presets_def) > 0 THEN
  ALTER TABLE alert_presets DROP CONSTRAINT alert_presets_metric_chk;
  EXECUTE 'ALTER TABLE alert_presets ADD CONSTRAINT alert_presets_metric_chk ' || replace(presets_def, ' OR (metric = ''ai_budget_alerts''::text)', '');
 END IF;
END $$;
-- +goose StatementEnd
RESET lock_timeout;
