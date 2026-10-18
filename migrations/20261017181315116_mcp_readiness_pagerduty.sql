-- +goose Up
CREATE TABLE IF NOT EXISTS mcp_readiness_pagerduty_policies (
 rule_id uuid PRIMARY KEY REFERENCES alert_rules(id) ON DELETE CASCADE,
 version bigint NOT NULL CHECK(version>0),
 enabled boolean NOT NULL,
 integration_key_sealed bytea NOT NULL CHECK(octet_length(integration_key_sealed)<=4096),
 fingerprint text NOT NULL CHECK(fingerprint='' OR fingerprint ~ '^[a-f0-9]{64}$'),
 CHECK((enabled AND octet_length(integration_key_sealed)>0 AND fingerprint<>'') OR (NOT enabled AND octet_length(integration_key_sealed)=0 AND fingerprint=''))
);
ALTER TABLE mcp_readiness_incidents DROP CONSTRAINT IF EXISTS mcp_readiness_pagerduty_route_check;
ALTER TABLE mcp_readiness_incidents ADD CONSTRAINT mcp_readiness_pagerduty_route_check CHECK (
 NOT (delivery_routing->'primary' ? 'provider') OR
 ((delivery_routing->'primary'->>'provider' IN ('webhook','pagerduty')) IS TRUE AND
 (delivery_routing->'primary'->>'provider'<>'pagerduty' OR
 (delivery_routing->'primary'->>'url'='https://events.pagerduty.com/v2/enqueue' AND delivery_routing->'primary'->>'receiver_id' ~ '^[a-f0-9]{64}$' AND length(delivery_routing->'primary'->>'secret')>0) IS TRUE))
);
-- +goose Down
-- +goose StatementBegin
DO $migration$ BEGIN
 IF EXISTS(SELECT 1 FROM mcp_readiness_pagerduty_policies)
 OR EXISTS(SELECT 1 FROM mcp_readiness_incidents WHERE delivery_routing->'primary'->>'provider'='pagerduty')
 OR EXISTS(SELECT 1 FROM mcp_readiness_incident_events WHERE destination_deliveries->'primary'->>'provider'='pagerduty') THEN
  RAISE EXCEPTION 'PagerDuty configuration and delivery history must be retained before rollback';
 END IF;
 ALTER TABLE mcp_readiness_incidents DROP CONSTRAINT mcp_readiness_pagerduty_route_check;
 DROP TABLE mcp_readiness_pagerduty_policies;
END $migration$;
-- +goose StatementEnd
