-- +goose Up
-- The final schema object marks this transactional Up as applied. A ledger
-- replay must preserve definitions widened by later migrations and must not
-- re-run grant backfills or replace saved rollback constraint definitions.
-- +goose StatementBegin
DO $mcp_replay_guard$ BEGIN
 IF EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='alert_rules'::regclass AND conname='alert_rules_mcp_catalog_chk') THEN RETURN; END IF;
CREATE TABLE mcp_catalog_monitors (
 app_id uuid PRIMARY KEY REFERENCES apps(id) ON DELETE CASCADE,
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 payload jsonb NOT NULL CHECK (jsonb_typeof(payload)='object'),
 CHECK (jsonb_typeof(payload->'history')='array' AND jsonb_array_length(payload->'history')<=32),
 CHECK (payload->'latest' IS NULL OR payload->'latest'='null'::jsonb OR payload->'latest'->>'state' IN ('healthy','drift','unavailable'))
);

DO $$ DECLARE rules_base text; presets_base text;
BEGIN
 SELECT pg_get_constraintdef(oid) INTO rules_base FROM pg_constraint WHERE conrelid='alert_rules'::regclass AND conname='alert_rules_metric_chk';
 SELECT pg_get_constraintdef(oid) INTO presets_base FROM pg_constraint WHERE conrelid='alert_presets'::regclass AND conname='alert_presets_metric_chk';
 EXECUTE format('CREATE FUNCTION mcp_catalog_monitor_metric_base(kind text) RETURNS text LANGUAGE sql IMMUTABLE AS %L',format('SELECT CASE WHEN $1=%L THEN %L ELSE %L END','rules',rules_base,presets_base));
 ALTER TABLE alert_rules DROP CONSTRAINT alert_rules_metric_chk;
 ALTER TABLE alert_presets DROP CONSTRAINT alert_presets_metric_chk;
 EXECUTE format('ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_metric_chk CHECK ((%s) OR metric IN (''mcp_catalog_drift'',''mcp_catalog_monitor_unavailable''))',regexp_replace(rules_base,'^CHECK \((.*)\)$','\1'));
 EXECUTE format('ALTER TABLE alert_presets ADD CONSTRAINT alert_presets_metric_chk CHECK ((%s) OR metric IN (''mcp_catalog_drift'',''mcp_catalog_monitor_unavailable''))',regexp_replace(presets_base,'^CHECK \((.*)\)$','\1'));
END $$;

ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_mcp_catalog_chk CHECK (metric NOT IN ('mcp_catalog_drift','mcp_catalog_monitor_unavailable') OR (app_id IS NOT NULL AND action='webhook'));
INSERT INTO alert_presets(name,display_name,description,category,metric,comparison,threshold,window_spec,default_cooldown_minutes,minimum_plan) VALUES ('mcp_catalog_drift','MCP catalog drift','The latest fresh catalog observation differs from the reviewed baseline.','infrastructure','mcp_catalog_drift','gt',0,'5m',15,'hobby'),('mcp_catalog_monitor_unavailable','MCP catalog monitor unavailable','An enabled monitor has missing, stale, future-dated or failed observations.','infrastructure','mcp_catalog_monitor_unavailable','gt',0,'5m',15,'hobby');
END $mcp_replay_guard$;
-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN IF EXISTS (SELECT 1 FROM alert_rules WHERE metric IN ('mcp_catalog_drift','mcp_catalog_monitor_unavailable')) THEN RAISE EXCEPTION 'remove MCP catalog monitoring rules before rollback'; END IF; END $$;
-- +goose StatementEnd
DELETE FROM alert_presets WHERE name IN ('mcp_catalog_drift','mcp_catalog_monitor_unavailable');
ALTER TABLE alert_rules DROP CONSTRAINT alert_rules_mcp_catalog_chk;
-- +goose StatementBegin
DO $$ BEGIN
 ALTER TABLE alert_rules DROP CONSTRAINT alert_rules_metric_chk;
 ALTER TABLE alert_presets DROP CONSTRAINT alert_presets_metric_chk;
 EXECUTE 'ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_metric_chk '||mcp_catalog_monitor_metric_base('rules');
 EXECUTE 'ALTER TABLE alert_presets ADD CONSTRAINT alert_presets_metric_chk '||mcp_catalog_monitor_metric_base('presets');
END $$;
-- +goose StatementEnd
DROP FUNCTION mcp_catalog_monitor_metric_base(text);
DROP TABLE mcp_catalog_monitors;
