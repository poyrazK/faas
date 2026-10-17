-- +goose NO TRANSACTION
-- +goose Up
SET lock_timeout = '5s';
CREATE INDEX CONCURRENTLY IF NOT EXISTS alert_deliveries_mcp_readiness_timeline_idx ON alert_deliveries (account_id, app_id, fired_at DESC, id DESC)
WHERE is_test=false AND payload->>'metric'='mcp_readiness_unhealthy' AND payload->>'event' IN ('alert.fired','alert.resolved') AND payload->>'incident_id' IS NOT NULL AND payload->>'event_id' IS NOT NULL;
-- A failed concurrent build can leave an invalid index. Do not record success
-- when IF NOT EXISTS skips one; require explicit cleanup before retrying.
-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_index WHERE indexrelid=to_regclass('alert_deliveries_mcp_readiness_timeline_idx') AND indisvalid AND indisready) THEN
  RAISE EXCEPTION 'readiness timeline index is invalid: DROP INDEX CONCURRENTLY alert_deliveries_mcp_readiness_timeline_idx and retry migration';
 END IF;
END $$;
-- +goose StatementEnd
RESET lock_timeout;
-- +goose Down
SET lock_timeout = '5s';
DROP INDEX CONCURRENTLY IF EXISTS alert_deliveries_mcp_readiness_timeline_idx;
RESET lock_timeout;
