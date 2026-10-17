-- +goose Up
-- +goose StatementBegin
DO $mcp_protocol_alerts$
DECLARE base text;
BEGIN
 IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='alert_rules'::regclass AND conname='alert_rules_mcp_protocol_chk') THEN RETURN; END IF;
 ALTER TABLE alert_rules ADD COLUMN mcp_min_calls integer NOT NULL DEFAULT 0;
 ALTER TABLE alert_rules ADD COLUMN mcp_for_minutes integer NOT NULL DEFAULT 0;
 SELECT pg_get_constraintdef(oid) INTO base FROM pg_constraint WHERE conrelid='alert_rules'::regclass AND conname='alert_rules_metric_chk';
 EXECUTE format('CREATE FUNCTION mcp_protocol_alert_metric_base() RETURNS text LANGUAGE sql IMMUTABLE AS %L',format('SELECT %L::text',base));
 ALTER TABLE alert_rules DROP CONSTRAINT alert_rules_metric_chk;
 EXECUTE format('ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_metric_chk CHECK ((%s) OR metric IN (''mcp_protocol_error_rate_pct'',''mcp_protocol_execution_p95_ms''))',regexp_replace(base,'^CHECK \((.*)\)$','\1'));
 ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_mcp_protocol_chk CHECK (
  (metric IN ('mcp_protocol_error_rate_pct','mcp_protocol_execution_p95_ms') AND app_id IS NOT NULL AND action='webhook'
   AND mcp_min_calls BETWEEN 20 AND 1000000 AND mcp_for_minutes BETWEEN 1 AND 60
   AND comparison IN ('gt','gte') AND window_spec IN ('5m','15m','1h')
   AND threshold >= 0 AND threshold <= CASE WHEN metric='mcp_protocol_error_rate_pct' THEN 100 ELSE 300000 END)
  OR (metric NOT IN ('mcp_protocol_error_rate_pct','mcp_protocol_execution_p95_ms') AND mcp_min_calls=0 AND mcp_for_minutes=0)
 );
END $mcp_protocol_alerts$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM alert_rules WHERE metric IN ('mcp_protocol_error_rate_pct','mcp_protocol_execution_p95_ms')) THEN
  RAISE EXCEPTION 'remove MCP protocol alert rules before rollback';
 END IF;
 ALTER TABLE alert_rules DROP CONSTRAINT alert_rules_mcp_protocol_chk;
 ALTER TABLE alert_rules DROP CONSTRAINT alert_rules_metric_chk;
 EXECUTE 'ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_metric_chk '||mcp_protocol_alert_metric_base();
 ALTER TABLE alert_rules DROP COLUMN mcp_min_calls;
 ALTER TABLE alert_rules DROP COLUMN mcp_for_minutes;
 DROP FUNCTION mcp_protocol_alert_metric_base();
END $$;
-- +goose StatementEnd
