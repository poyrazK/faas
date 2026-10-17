-- +goose Up
SET LOCAL lock_timeout = '5s';
-- Hydrated lazily from existing routing, leases and retry deadlines on first use.
-- Stop readiness evaluators and upgrade them together before resuming dispatch.
ALTER TABLE mcp_readiness_incident_events ADD COLUMN IF NOT EXISTS destination_deliveries jsonb NOT NULL DEFAULT '{}'
 CHECK(jsonb_typeof(destination_deliveries)='object' AND destination_deliveries-ARRAY['primary','fallback']='{}');
ALTER TABLE mcp_readiness_notification_actions ADD COLUMN IF NOT EXISTS destination text NOT NULL DEFAULT '' CHECK(destination IN ('','primary','fallback'));
ALTER TABLE alert_deliveries DROP CONSTRAINT IF EXISTS alert_deliveries_readiness_failover_reason_check;
ALTER TABLE alert_deliveries ADD CONSTRAINT alert_deliveries_readiness_failover_reason_check CHECK(readiness_failover_reason IN ('','primary_failure_threshold','incident_sticky_fallback','recovery_to_primary','primary_queue_blocked'));

-- +goose Down
-- +goose StatementBegin
DO $migration$ BEGIN
 IF EXISTS(SELECT 1 FROM mcp_readiness_incident_events WHERE destination_deliveries<>'{}')
 OR EXISTS(SELECT 1 FROM mcp_readiness_notification_actions WHERE destination<>'')
 OR EXISTS(SELECT 1 FROM alert_deliveries WHERE readiness_failover_reason='primary_queue_blocked') THEN
  RAISE EXCEPTION 'readiness destination delivery history must be retained before rollback';
 END IF;
 ALTER TABLE mcp_readiness_incident_events DROP COLUMN destination_deliveries;
 ALTER TABLE mcp_readiness_notification_actions DROP COLUMN destination;
 ALTER TABLE alert_deliveries DROP CONSTRAINT alert_deliveries_readiness_failover_reason_check;
 ALTER TABLE alert_deliveries ADD CONSTRAINT alert_deliveries_readiness_failover_reason_check CHECK(readiness_failover_reason IN ('','primary_failure_threshold','incident_sticky_fallback','recovery_to_primary'));
END $migration$;
-- +goose StatementEnd
