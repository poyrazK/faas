-- +goose Up
-- +goose StatementBegin
DO $$ DECLARE rules_base text; presets_base text;
BEGIN
 SELECT pg_get_constraintdef(oid) INTO rules_base FROM pg_constraint WHERE conrelid='alert_rules'::regclass AND conname='alert_rules_metric_chk';
 SELECT pg_get_constraintdef(oid) INTO presets_base FROM pg_constraint WHERE conrelid='alert_presets'::regclass AND conname='alert_presets_metric_chk';
 EXECUTE format('CREATE FUNCTION mcp_connection_alert_metric_base(kind text) RETURNS text LANGUAGE sql IMMUTABLE AS %L',format('SELECT CASE WHEN $1=%L THEN %L ELSE %L END','rules',rules_base,presets_base));
 ALTER TABLE alert_rules DROP CONSTRAINT alert_rules_metric_chk;
 ALTER TABLE alert_presets DROP CONSTRAINT alert_presets_metric_chk;
 EXECUTE format('ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_metric_chk CHECK ((%s) OR metric IN (''mcp_oauth_reauthorization_required'',''mcp_oauth_renewal_failed'',''mcp_oauth_retirement_expiring'',''mcp_oauth_issuance_unknown''))',regexp_replace(rules_base,'^CHECK \((.*)\)$','\1'));
 EXECUTE format('ALTER TABLE alert_presets ADD CONSTRAINT alert_presets_metric_chk CHECK ((%s) OR metric IN (''mcp_oauth_reauthorization_required'',''mcp_oauth_renewal_failed'',''mcp_oauth_retirement_expiring'',''mcp_oauth_issuance_unknown''))',regexp_replace(presets_base,'^CHECK \((.*)\)$','\1'));
END $$;
-- +goose StatementEnd
ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_mcp_connection_health_action_chk CHECK (metric NOT IN ('mcp_oauth_reauthorization_required','mcp_oauth_renewal_failed','mcp_oauth_retirement_expiring','mcp_oauth_issuance_unknown') OR action='webhook');
INSERT INTO alert_presets(name,display_name,description,category,metric,comparison,threshold,window_spec,default_cooldown_minutes,minimum_plan) VALUES
('mcp_oauth_reauthorization_required','MCP connection needs authorization','An observer role requires reconnection or restoration of a required credential.','infrastructure','mcp_oauth_reauthorization_required','gt',0,'5m',15,'hobby'),
('mcp_oauth_renewal_failed','MCP OAuth renewal failed','An active observer has failed renewal or unavailable OAuth credentials.','infrastructure','mcp_oauth_renewal_failed','gt',0,'5m',15,'hobby'),
('mcp_oauth_retirement_expiring','MCP retirement retention warning','Unresolved retirement credentials expire within 24 hours or have already expired.','infrastructure','mcp_oauth_retirement_expiring','gt',0,'5m',15,'hobby'),
('mcp_oauth_issuance_unknown','MCP OAuth issuance unknown','A lost exchange response or expired exchange lease requires provider-side investigation.','infrastructure','mcp_oauth_issuance_unknown','gt',0,'5m',15,'hobby');

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM alert_rules WHERE metric IN ('mcp_oauth_reauthorization_required','mcp_oauth_renewal_failed','mcp_oauth_retirement_expiring','mcp_oauth_issuance_unknown')) THEN RAISE EXCEPTION 'remove MCP connection health alert rules before rollback'; END IF;
 DELETE FROM alert_presets WHERE name IN ('mcp_oauth_reauthorization_required','mcp_oauth_renewal_failed','mcp_oauth_retirement_expiring','mcp_oauth_issuance_unknown');
 ALTER TABLE alert_rules DROP CONSTRAINT alert_rules_mcp_connection_health_action_chk;
 ALTER TABLE alert_rules DROP CONSTRAINT alert_rules_metric_chk;
 ALTER TABLE alert_presets DROP CONSTRAINT alert_presets_metric_chk;
 EXECUTE 'ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_metric_chk '||mcp_connection_alert_metric_base('rules');
 EXECUTE 'ALTER TABLE alert_presets ADD CONSTRAINT alert_presets_metric_chk '||mcp_connection_alert_metric_base('presets');
END $$;
-- +goose StatementEnd
DROP FUNCTION mcp_connection_alert_metric_base(text);
