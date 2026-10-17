-- +goose Up
CREATE INDEX IF NOT EXISTS alert_deliveries_mcp_readiness_timeline_idx ON alert_deliveries (account_id, app_id, fired_at DESC, id DESC)
WHERE is_test=false AND payload->>'metric'='mcp_readiness_unhealthy' AND payload->>'event' IN ('alert.fired','alert.resolved') AND payload->>'incident_id' IS NOT NULL AND payload->>'event_id' IS NOT NULL;
-- +goose Down
DROP INDEX IF EXISTS alert_deliveries_mcp_readiness_timeline_idx;
