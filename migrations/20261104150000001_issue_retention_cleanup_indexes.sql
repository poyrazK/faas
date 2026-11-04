-- migration-compat: expand
-- +goose NO TRANSACTION
-- +goose Up
SET lock_timeout = '5s';
-- A failed concurrent build can leave an invalid index under this name.
DROP INDEX CONCURRENTLY IF EXISTS issue_events_global_retention_idx;
CREATE INDEX CONCURRENTLY IF NOT EXISTS issue_events_global_retention_idx ON issue_events (received_at, app_id);
DROP INDEX CONCURRENTLY IF EXISTS issue_ingest_tokens_expiry_idx;
CREATE INDEX CONCURRENTLY IF NOT EXISTS issue_ingest_tokens_expiry_idx ON issue_ingest_tokens (expires_at, id);
RESET lock_timeout;
-- +goose Down
SET lock_timeout = '5s';
DROP INDEX CONCURRENTLY IF EXISTS issue_ingest_tokens_expiry_idx;
DROP INDEX CONCURRENTLY IF EXISTS issue_events_global_retention_idx;
RESET lock_timeout;
