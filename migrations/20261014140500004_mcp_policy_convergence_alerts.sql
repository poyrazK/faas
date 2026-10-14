-- +goose Up
-- The final schema object marks this transactional Up as applied. A ledger
-- replay must preserve definitions widened by later migrations and must not
-- re-run grant backfills or replace saved rollback constraint definitions.
-- +goose StatementBegin
DO $mcp_replay_guard$ BEGIN
 IF EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='alert_rules'::regclass AND conname='alert_rules_mcp_convergence_chk') THEN RETURN; END IF;
DO $$ DECLARE rules_base text; presets_base text;
BEGIN
 SELECT pg_get_constraintdef(oid) INTO rules_base FROM pg_constraint WHERE conrelid='alert_rules'::regclass AND conname='alert_rules_metric_chk';
 SELECT pg_get_constraintdef(oid) INTO presets_base FROM pg_constraint WHERE conrelid='alert_presets'::regclass AND conname='alert_presets_metric_chk';
 EXECUTE format('CREATE FUNCTION mcp_policy_convergence_metric_base(kind text) RETURNS text LANGUAGE sql IMMUTABLE AS %L',format('SELECT CASE WHEN $1=%L THEN %L ELSE %L END','rules',rules_base,presets_base));
 ALTER TABLE alert_rules DROP CONSTRAINT alert_rules_metric_chk;
 ALTER TABLE alert_presets DROP CONSTRAINT alert_presets_metric_chk;
 EXECUTE format('ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_metric_chk CHECK ((%s) OR metric IN (''mcp_policy_unconverged_gateways'',''mcp_policy_unverified''))',regexp_replace(rules_base,'^CHECK \((.*)\)$','\1'));
 EXECUTE format('ALTER TABLE alert_presets ADD CONSTRAINT alert_presets_metric_chk CHECK ((%s) OR metric IN (''mcp_policy_unconverged_gateways'',''mcp_policy_unverified''))',regexp_replace(presets_base,'^CHECK \((.*)\)$','\1'));
END $$;

ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_mcp_convergence_chk CHECK (metric NOT IN ('mcp_policy_unconverged_gateways','mcp_policy_unverified') OR (app_id IS NOT NULL AND action='webhook'));
INSERT INTO alert_presets(name,display_name,description,category,metric,comparison,threshold,window_spec,default_cooldown_minutes,minimum_plan) VALUES ('mcp_policy_replay_pending','MCP gateway policy replay pending','A serving gateway has stale or missing policy observations or has not replayed the current account revision. Instantaneous observation, not a sustained window.','infrastructure','mcp_policy_unconverged_gateways','gt',0,'5m',15,'hobby'),('mcp_policy_unverified','MCP gateway policy unverified','An active HTTP app has no serving gateway or no established account edge-rule revision.','infrastructure','mcp_policy_unverified','gt',0,'5m',15,'hobby');
END $mcp_replay_guard$;
-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN IF EXISTS (SELECT 1 FROM alert_rules WHERE metric IN ('mcp_policy_unconverged_gateways','mcp_policy_unverified')) THEN RAISE EXCEPTION 'remove MCP convergence rules before rollback'; END IF; END $$;
-- +goose StatementEnd
DELETE FROM alert_presets WHERE name IN ('mcp_policy_replay_pending','mcp_policy_unverified');
ALTER TABLE alert_rules DROP CONSTRAINT alert_rules_mcp_convergence_chk;
-- +goose StatementBegin
DO $$ BEGIN
 ALTER TABLE alert_rules DROP CONSTRAINT alert_rules_metric_chk;
 ALTER TABLE alert_presets DROP CONSTRAINT alert_presets_metric_chk;
 EXECUTE 'ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_metric_chk '||mcp_policy_convergence_metric_base('rules');
 EXECUTE 'ALTER TABLE alert_presets ADD CONSTRAINT alert_presets_metric_chk '||mcp_policy_convergence_metric_base('presets');
END $$;
-- +goose StatementEnd
DROP FUNCTION mcp_policy_convergence_metric_base(text);
