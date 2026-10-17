-- +goose Up
SET LOCAL lock_timeout = '5s';
CREATE TABLE IF NOT EXISTS mcp_readiness_fallback_policies (
 rule_id uuid PRIMARY KEY REFERENCES alert_rules(id) ON DELETE CASCADE,
 version bigint NOT NULL CHECK(version>0),
 enabled boolean NOT NULL,
 webhook_url text NOT NULL CHECK(octet_length(webhook_url)<=2048),
 webhook_secret_sealed bytea NOT NULL,
 after_failures integer NOT NULL CHECK(after_failures BETWEEN 0 AND 10),
 CHECK((enabled AND webhook_url<>'' AND octet_length(webhook_secret_sealed)>0 AND after_failures>0) OR (NOT enabled AND webhook_url='' AND octet_length(webhook_secret_sealed)=0 AND after_failures=0))
);
ALTER TABLE mcp_readiness_incidents ADD COLUMN IF NOT EXISTS delivery_routing jsonb NOT NULL DEFAULT '{}' CHECK(jsonb_typeof(delivery_routing)='object');
ALTER TABLE mcp_readiness_incident_events ADD COLUMN IF NOT EXISTS delivery_routing jsonb NOT NULL DEFAULT '{}' CHECK(jsonb_typeof(delivery_routing)='object');
-- Preserve live-primary behavior for incidents already attempted by older workers.
-- Unknown outcomes may have reached primary, so include it in recovery routing.
UPDATE mcp_readiness_incidents i SET delivery_routing=jsonb_build_object('initialized',true,'primary_informed',EXISTS(SELECT 1 FROM mcp_readiness_incident_events e WHERE e.incident_id=i.id AND e.kind='alert.fired' AND e.attempt_count>0))
WHERE i.delivery_routing='{}' AND EXISTS(SELECT 1 FROM mcp_readiness_incident_events e WHERE e.incident_id=i.id AND e.attempt_count>0);
ALTER TABLE alert_deliveries ADD COLUMN IF NOT EXISTS readiness_event_id text NOT NULL DEFAULT '' CHECK(readiness_event_id='' OR readiness_event_id ~ '^[0-9a-f-]{36}$');
ALTER TABLE alert_deliveries ADD COLUMN IF NOT EXISTS readiness_destination text NOT NULL DEFAULT '' CHECK(readiness_destination IN ('','primary','fallback'));
ALTER TABLE alert_deliveries ADD COLUMN IF NOT EXISTS readiness_failover_reason text NOT NULL DEFAULT '' CHECK(readiness_failover_reason IN ('','primary_failure_threshold','incident_sticky_fallback','recovery_to_primary'));

-- +goose Down
-- +goose StatementBegin
DO $migration$ BEGIN
 IF EXISTS(SELECT 1 FROM mcp_readiness_fallback_policies)
 OR EXISTS(SELECT 1 FROM mcp_readiness_incidents WHERE delivery_routing<>'{}')
 OR EXISTS(SELECT 1 FROM mcp_readiness_incident_events WHERE delivery_routing<>'{}')
 OR EXISTS(SELECT 1 FROM alert_deliveries WHERE readiness_destination<>'') THEN
  RAISE EXCEPTION 'readiness fallback routing history must be retained before rollback';
 END IF;
 DROP TABLE mcp_readiness_fallback_policies;
 ALTER TABLE mcp_readiness_incidents DROP COLUMN delivery_routing;
 ALTER TABLE mcp_readiness_incident_events DROP COLUMN delivery_routing;
 ALTER TABLE alert_deliveries DROP COLUMN readiness_event_id;
 ALTER TABLE alert_deliveries DROP COLUMN readiness_destination;
 ALTER TABLE alert_deliveries DROP COLUMN readiness_failover_reason;
END $migration$;
-- +goose StatementEnd
