-- +goose Up
ALTER TABLE mcp_readiness_pagerduty_policies ADD COLUMN IF NOT EXISTS region text NOT NULL DEFAULT 'us' CHECK(region IN ('us','eu'));
ALTER TABLE mcp_readiness_pagerduty_policies DROP CONSTRAINT IF EXISTS mcp_readiness_pagerduty_disabled_region_check;
ALTER TABLE mcp_readiness_pagerduty_policies ADD CONSTRAINT mcp_readiness_pagerduty_disabled_region_check CHECK(enabled OR region='us');
ALTER TABLE mcp_readiness_incidents DROP CONSTRAINT IF EXISTS mcp_readiness_pagerduty_route_check;
ALTER TABLE mcp_readiness_incidents ADD CONSTRAINT mcp_readiness_pagerduty_route_check CHECK (
 NOT (delivery_routing->'primary' ? 'provider') OR
 ((delivery_routing->'primary'->>'provider' IN ('webhook','pagerduty')) IS TRUE AND
 (delivery_routing->'primary'->>'provider'<>'pagerduty' OR
 (delivery_routing->'primary'->>'url' IN ('https://events.pagerduty.com/v2/enqueue','https://events.eu.pagerduty.com/v2/enqueue') AND delivery_routing->'primary'->>'receiver_id' ~ '^[a-f0-9]{64}$' AND length(delivery_routing->'primary'->>'secret')>0) IS TRUE))
);
-- +goose Down
-- +goose StatementBegin
DO $migration$ BEGIN
 IF EXISTS(SELECT 1 FROM mcp_readiness_pagerduty_policies WHERE region='eu')
 OR EXISTS(SELECT 1 FROM mcp_readiness_incidents WHERE delivery_routing->'primary'->>'provider'='pagerduty' AND delivery_routing->'primary'->>'url'='https://events.eu.pagerduty.com/v2/enqueue') THEN
  RAISE EXCEPTION 'EU PagerDuty configuration and pinned delivery history must be retained before rollback';
 END IF;
 ALTER TABLE mcp_readiness_incidents DROP CONSTRAINT mcp_readiness_pagerduty_route_check;
 ALTER TABLE mcp_readiness_incidents ADD CONSTRAINT mcp_readiness_pagerduty_route_check CHECK (
  NOT (delivery_routing->'primary' ? 'provider') OR
  ((delivery_routing->'primary'->>'provider' IN ('webhook','pagerduty')) IS TRUE AND
  (delivery_routing->'primary'->>'provider'<>'pagerduty' OR
  (delivery_routing->'primary'->>'url'='https://events.pagerduty.com/v2/enqueue' AND delivery_routing->'primary'->>'receiver_id' ~ '^[a-f0-9]{64}$' AND length(delivery_routing->'primary'->>'secret')>0) IS TRUE))
 );
 ALTER TABLE mcp_readiness_pagerduty_policies DROP CONSTRAINT mcp_readiness_pagerduty_disabled_region_check;
 ALTER TABLE mcp_readiness_pagerduty_policies DROP COLUMN region;
END $migration$;
-- +goose StatementEnd
