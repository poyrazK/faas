-- +goose Up
SET LOCAL lock_timeout = '5s';
-- The final schema object marks this transactional Up as applied. A ledger
-- replay must preserve definitions widened by later migrations and must not
-- re-run grant backfills or replace saved rollback constraint definitions.
-- +goose StatementBegin
DO $mcp_replay_guard$ BEGIN
 IF EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='alert_rules'::regclass AND conname='alert_rules_mcp_readiness_action_chk') THEN RETURN; END IF;
DO $$ DECLARE rules_base text; presets_base text;
BEGIN
 SELECT pg_get_constraintdef(oid) INTO rules_base FROM pg_constraint WHERE conrelid='alert_rules'::regclass AND conname='alert_rules_metric_chk';
 SELECT pg_get_constraintdef(oid) INTO presets_base FROM pg_constraint WHERE conrelid='alert_presets'::regclass AND conname='alert_presets_metric_chk';
 EXECUTE format('CREATE FUNCTION mcp_readiness_alert_metric_base(kind text) RETURNS text LANGUAGE sql IMMUTABLE AS %L',format('SELECT CASE WHEN $1=%L THEN %L ELSE %L END','rules',rules_base,presets_base));
 ALTER TABLE alert_rules DROP CONSTRAINT alert_rules_metric_chk;
 ALTER TABLE alert_presets DROP CONSTRAINT alert_presets_metric_chk;
 EXECUTE format('ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_metric_chk CHECK ((%s) OR metric IN (''mcp_readiness_unhealthy''))',regexp_replace(rules_base,'^CHECK \((.*)\)$','\1'));
 EXECUTE format('ALTER TABLE alert_presets ADD CONSTRAINT alert_presets_metric_chk CHECK ((%s) OR metric IN (''mcp_readiness_unhealthy''))',regexp_replace(presets_base,'^CHECK \((.*)\)$','\1'));
END $$;

ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_mcp_readiness_action_chk CHECK (metric NOT IN ('mcp_readiness_unhealthy') OR (action='webhook' AND app_id IS NOT NULL));
INSERT INTO alert_presets(name,display_name,description,category,metric,comparison,threshold,window_spec,default_cooldown_minutes,minimum_plan) VALUES
('mcp_readiness_unhealthy','MCP readiness monitor unhealthy','Serving deployment roles exceeded consecutive-failure thresholds or monitoring is stale.','infrastructure','mcp_readiness_unhealthy','gt',0,'5m',15,'hobby');
END $mcp_replay_guard$;
-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM alert_rules WHERE metric IN ('mcp_readiness_unhealthy')) THEN RAISE EXCEPTION 'remove MCP readiness alert rules before rollback'; END IF;
 DELETE FROM alert_presets WHERE name IN ('mcp_readiness_unhealthy');
 ALTER TABLE alert_rules DROP CONSTRAINT alert_rules_mcp_readiness_action_chk;
 ALTER TABLE alert_rules DROP CONSTRAINT alert_rules_metric_chk;
 ALTER TABLE alert_presets DROP CONSTRAINT alert_presets_metric_chk;
 EXECUTE 'ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_metric_chk '||mcp_readiness_alert_metric_base('rules');
 EXECUTE 'ALTER TABLE alert_presets ADD CONSTRAINT alert_presets_metric_chk '||mcp_readiness_alert_metric_base('presets');
END $$;
-- +goose StatementEnd
DROP FUNCTION mcp_readiness_alert_metric_base(text);
