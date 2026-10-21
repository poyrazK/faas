-- migration-compat: contract
-- filename: 20261020161647152_route_insight_alerts.sql

-- ADR-20261020161310: route_insights_detected counts an app's route insights
-- that became active within the rule's window, so notification channels can
-- deliver new insights. Each metric CHECK is replaced online: a widened copy
-- of its current definition (which keeps metrics other migrations added) is
-- added NOT VALID and validated, the old one is dropped, and the copy takes
-- the old name so later migrations find it. Every step commits on its own and
-- is guarded, so a rerun after a partial failure or a replay over an already
-- migrated schema converges without narrowing a later widening. The "New route
-- insights" preset puts the rule in the dashboard preset grid. The previous
-- release survives every step, but RENAME CONSTRAINT is a contract statement
-- to migration-lint, so a full release applies this migration.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';
-- +goose StatementBegin
DO $$ DECLARE rules_base text; presets_base text;
BEGIN
 IF EXISTS(SELECT 1 FROM pg_proc WHERE proname = 'route_insight_alert_metric_base') THEN RETURN; END IF;
 SELECT pg_get_constraintdef(oid) INTO rules_base FROM pg_constraint WHERE conrelid = 'alert_rules'::regclass AND conname = 'alert_rules_metric_chk';
 SELECT pg_get_constraintdef(oid) INTO presets_base FROM pg_constraint WHERE conrelid = 'alert_presets'::regclass AND conname = 'alert_presets_metric_chk';
 EXECUTE format('CREATE FUNCTION route_insight_alert_metric_base(kind text) RETURNS text LANGUAGE sql IMMUTABLE AS %L',
   format('SELECT CASE WHEN $1=%L THEN %L ELSE %L END', 'rules', rules_base, presets_base));
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid = 'alert_rules'::regclass AND conname = 'alert_rules_metric_next_chk')
    AND position('route_insights_detected' in pg_get_constraintdef((SELECT oid FROM pg_constraint WHERE conrelid = 'alert_rules'::regclass AND conname = 'alert_rules_metric_chk'))) = 0 THEN
  EXECUTE format('ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_metric_next_chk CHECK ((%s) OR metric IN (''route_insights_detected'')) NOT VALID',
    regexp_replace(route_insight_alert_metric_base('rules'), '^CHECK \((.*)\)$', '\1'));
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid = 'alert_presets'::regclass AND conname = 'alert_presets_metric_next_chk')
    AND position('route_insights_detected' in pg_get_constraintdef((SELECT oid FROM pg_constraint WHERE conrelid = 'alert_presets'::regclass AND conname = 'alert_presets_metric_chk'))) = 0 THEN
  EXECUTE format('ALTER TABLE alert_presets ADD CONSTRAINT alert_presets_metric_next_chk CHECK ((%s) OR metric IN (''route_insights_detected'')) NOT VALID',
    regexp_replace(route_insight_alert_metric_base('presets'), '^CHECK \((.*)\)$', '\1'));
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid = 'alert_rules'::regclass AND conname = 'alert_rules_metric_next_chk') THEN
  ALTER TABLE alert_rules VALIDATE CONSTRAINT alert_rules_metric_next_chk;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid = 'alert_presets'::regclass AND conname = 'alert_presets_metric_next_chk') THEN
  ALTER TABLE alert_presets VALIDATE CONSTRAINT alert_presets_metric_next_chk;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid = 'alert_rules'::regclass AND conname = 'alert_rules_route_insights_chk') THEN
  ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_route_insights_chk CHECK (metric <> 'route_insights_detected'
    OR (app_id IS NOT NULL AND event_subscription_id IS NULL AND action = 'webhook' AND window_spec IN ('6h', '24h', '7d'))) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE alert_rules VALIDATE CONSTRAINT alert_rules_route_insights_chk;
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid = 'alert_rules'::regclass AND conname = 'alert_rules_metric_next_chk') THEN
  ALTER TABLE alert_rules DROP CONSTRAINT IF EXISTS alert_rules_metric_chk;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid = 'alert_rules'::regclass AND conname = 'alert_rules_metric_next_chk') THEN
  ALTER TABLE alert_rules RENAME CONSTRAINT alert_rules_metric_next_chk TO alert_rules_metric_chk;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid = 'alert_presets'::regclass AND conname = 'alert_presets_metric_next_chk') THEN
  ALTER TABLE alert_presets DROP CONSTRAINT IF EXISTS alert_presets_metric_chk;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid = 'alert_presets'::regclass AND conname = 'alert_presets_metric_next_chk') THEN
  ALTER TABLE alert_presets RENAME CONSTRAINT alert_presets_metric_next_chk TO alert_presets_metric_chk;
 END IF;
END $$;
-- +goose StatementEnd
INSERT INTO alert_presets(name, display_name, description, category, metric, comparison, threshold, window_spec, default_cooldown_minutes, minimum_plan)
SELECT 'route_insights_detected', 'New route insights',
       'A route started keeping the app awake, became safe to cache, or has a cache rule serving stale or too-short responses.',
       'cost', 'route_insights_detected', 'gt', 0, '24h', 360, 'hobby'
WHERE NOT EXISTS (SELECT 1 FROM alert_presets WHERE name = 'route_insights_detected');
RESET lock_timeout;

-- +goose Down
SET lock_timeout = '5s';
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM alert_rules WHERE metric = 'route_insights_detected') THEN RAISE EXCEPTION 'remove route insight alert rules before rollback'; END IF;
 DELETE FROM alert_presets WHERE name = 'route_insights_detected';
 ALTER TABLE alert_rules DROP CONSTRAINT IF EXISTS alert_rules_route_insights_chk;
 ALTER TABLE alert_rules DROP CONSTRAINT alert_rules_metric_chk;
 ALTER TABLE alert_presets DROP CONSTRAINT alert_presets_metric_chk;
 EXECUTE 'ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_metric_chk ' || route_insight_alert_metric_base('rules');
 EXECUTE 'ALTER TABLE alert_presets ADD CONSTRAINT alert_presets_metric_chk ' || route_insight_alert_metric_base('presets');
END $$;
-- +goose StatementEnd
DROP FUNCTION IF EXISTS route_insight_alert_metric_base(text);
RESET lock_timeout;
