-- filename: 20261028140007417_route_alert_metrics.sql
-- migration-compat: expand
-- ADR-20261028110044: production route monitor incidents and canary route
-- health holds reach alert rules and channels, not only app webhooks.
-- route_monitor_incidents counts the app's monitor incidents open at any time
-- within the window; route_health_holds counts the app's canaries the route
-- health guard held or aborted within the window. Both are app-scoped and
-- notification-only, with windows up to 24 hours. Two presets put them in the
-- dashboard grid.
--
-- alert_rules_route_alerts_chk is added NOT VALID and validated first. Each
-- metric CHECK is then replaced online: a widened copy of its current
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
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'alert_rules'::regclass AND conname = 'alert_rules_route_alerts_chk') THEN
  ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_route_alerts_chk CHECK ((metric NOT IN ('route_monitor_incidents', 'route_health_holds'))
    OR ((app_id IS NOT NULL) AND (event_subscription_id IS NULL) AND (action = 'webhook')
      AND (window_spec IN ('5m', '15m', '1h', '6h', '24h')))) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE alert_rules VALIDATE CONSTRAINT alert_rules_route_alerts_chk;
-- +goose StatementBegin
DO $$ DECLARE base text;
BEGIN
 IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'alert_rules'::regclass AND conname = 'alert_rules_metric_route_alerts_chk') THEN RETURN; END IF;
 SELECT pg_get_constraintdef(oid) INTO base FROM pg_constraint WHERE conrelid = 'alert_rules'::regclass AND conname = 'alert_rules_metric_chk';
 IF base IS NULL OR position('route_monitor_incidents' in base) > 0 THEN RETURN; END IF;
 EXECUTE format('ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_metric_route_alerts_chk CHECK ((%s) OR metric IN (%L, %L)) NOT VALID',
   regexp_replace(base, '^CHECK \((.*)\)$', '\1'), 'route_monitor_incidents', 'route_health_holds');
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'alert_rules'::regclass AND conname = 'alert_rules_metric_route_alerts_chk') THEN
  ALTER TABLE alert_rules VALIDATE CONSTRAINT alert_rules_metric_route_alerts_chk;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'alert_rules'::regclass AND conname = 'alert_rules_metric_route_alerts_chk') THEN
  ALTER TABLE alert_rules DROP CONSTRAINT IF EXISTS alert_rules_metric_chk;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $$ DECLARE widened text;
BEGIN
 IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'alert_rules'::regclass AND conname = 'alert_rules_metric_chk') THEN RETURN; END IF;
 SELECT pg_get_constraintdef(oid) INTO widened FROM pg_constraint WHERE conrelid = 'alert_rules'::regclass AND conname = 'alert_rules_metric_route_alerts_chk';
 EXECUTE 'ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_metric_chk ' || widened || ' NOT VALID';
END $$;
-- +goose StatementEnd
ALTER TABLE alert_rules VALIDATE CONSTRAINT alert_rules_metric_chk;
ALTER TABLE alert_rules DROP CONSTRAINT IF EXISTS alert_rules_metric_route_alerts_chk;
-- +goose StatementBegin
DO $$ DECLARE base text;
BEGIN
 IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'alert_presets'::regclass AND conname = 'alert_presets_metric_route_alerts_chk') THEN RETURN; END IF;
 SELECT pg_get_constraintdef(oid) INTO base FROM pg_constraint WHERE conrelid = 'alert_presets'::regclass AND conname = 'alert_presets_metric_chk';
 IF base IS NULL OR position('route_monitor_incidents' in base) > 0 THEN RETURN; END IF;
 EXECUTE format('ALTER TABLE alert_presets ADD CONSTRAINT alert_presets_metric_route_alerts_chk CHECK ((%s) OR metric IN (%L, %L)) NOT VALID',
   regexp_replace(base, '^CHECK \((.*)\)$', '\1'), 'route_monitor_incidents', 'route_health_holds');
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'alert_presets'::regclass AND conname = 'alert_presets_metric_route_alerts_chk') THEN
  ALTER TABLE alert_presets VALIDATE CONSTRAINT alert_presets_metric_route_alerts_chk;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'alert_presets'::regclass AND conname = 'alert_presets_metric_route_alerts_chk') THEN
  ALTER TABLE alert_presets DROP CONSTRAINT IF EXISTS alert_presets_metric_chk;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $$ DECLARE widened text;
BEGIN
 IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'alert_presets'::regclass AND conname = 'alert_presets_metric_chk') THEN RETURN; END IF;
 SELECT pg_get_constraintdef(oid) INTO widened FROM pg_constraint WHERE conrelid = 'alert_presets'::regclass AND conname = 'alert_presets_metric_route_alerts_chk';
 EXECUTE 'ALTER TABLE alert_presets ADD CONSTRAINT alert_presets_metric_chk ' || widened || ' NOT VALID';
END $$;
-- +goose StatementEnd
ALTER TABLE alert_presets VALIDATE CONSTRAINT alert_presets_metric_chk;
ALTER TABLE alert_presets DROP CONSTRAINT IF EXISTS alert_presets_metric_route_alerts_chk;
INSERT INTO alert_presets(name, display_name, description, category, metric, comparison, threshold, window_spec, default_cooldown_minutes, minimum_plan)
SELECT 'route_monitor_incidents', 'Route monitor incident',
       'A production route monitor opened an incident: a monitored route broke its error or latency budget.',
       'reliability', 'route_monitor_incidents', 'gt', 0, '15m', 30, 'hobby'
WHERE NOT EXISTS (SELECT 1 FROM alert_presets WHERE name = 'route_monitor_incidents');
INSERT INTO alert_presets(name, display_name, description, category, metric, comparison, threshold, window_spec, default_cooldown_minutes, minimum_plan)
SELECT 'route_health_holds', 'Canary held by route health',
       'The critical route health guard held or aborted a canary because a selected route regressed or lacked evidence.',
       'reliability', 'route_health_holds', 'gt', 0, '15m', 30, 'hobby'
WHERE NOT EXISTS (SELECT 1 FROM alert_presets WHERE name = 'route_health_holds');
RESET lock_timeout;

-- +goose Down
SET lock_timeout = '5s';
-- +goose StatementBegin
DO $$ DECLARE rules_def text; presets_def text;
BEGIN
 IF EXISTS (SELECT 1 FROM alert_rules WHERE metric IN ('route_monitor_incidents', 'route_health_holds')) THEN
  RAISE EXCEPTION 'delete route_monitor_incidents and route_health_holds alert rules before rollback';
 END IF;
 DELETE FROM alert_presets WHERE name IN ('route_monitor_incidents', 'route_health_holds');
 ALTER TABLE alert_rules DROP CONSTRAINT IF EXISTS alert_rules_route_alerts_chk;
 SELECT pg_get_constraintdef(oid) INTO rules_def FROM pg_constraint WHERE conrelid = 'alert_rules'::regclass AND conname = 'alert_rules_metric_chk';
 IF position(' OR (metric = ANY (ARRAY[''route_monitor_incidents''::text, ''route_health_holds''::text]))' in rules_def) > 0 THEN
  ALTER TABLE alert_rules DROP CONSTRAINT alert_rules_metric_chk;
  EXECUTE 'ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_metric_chk ' || replace(rules_def, ' OR (metric = ANY (ARRAY[''route_monitor_incidents''::text, ''route_health_holds''::text]))', '');
 END IF;
 SELECT pg_get_constraintdef(oid) INTO presets_def FROM pg_constraint WHERE conrelid = 'alert_presets'::regclass AND conname = 'alert_presets_metric_chk';
 IF position(' OR (metric = ANY (ARRAY[''route_monitor_incidents''::text, ''route_health_holds''::text]))' in presets_def) > 0 THEN
  ALTER TABLE alert_presets DROP CONSTRAINT alert_presets_metric_chk;
  EXECUTE 'ALTER TABLE alert_presets ADD CONSTRAINT alert_presets_metric_chk ' || replace(presets_def, ' OR (metric = ANY (ARRAY[''route_monitor_incidents''::text, ''route_health_holds''::text]))', '');
 END IF;
END $$;
-- +goose StatementEnd
RESET lock_timeout;
