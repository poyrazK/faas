-- migration-compat: expand
-- +goose NO TRANSACTION
-- +goose Up
SET lock_timeout = '5s';
SET statement_timeout = 0;
-- A failed concurrent build can leave an invalid index under either name.
-- Preserve valid indexes when a nontransactional migration is retried.
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM pg_index WHERE indexrelid = to_regclass('issue_events_global_retention_idx') AND NOT indisvalid) THEN
  DROP INDEX issue_events_global_retention_idx;
 END IF;
 IF EXISTS (SELECT 1 FROM pg_index WHERE indexrelid = to_regclass('issue_ingest_tokens_expiry_idx') AND NOT indisvalid) THEN
  DROP INDEX issue_ingest_tokens_expiry_idx;
 END IF;
END $$;
-- +goose StatementEnd
CREATE INDEX CONCURRENTLY IF NOT EXISTS issue_events_global_retention_idx ON issue_events (received_at, app_id);
CREATE INDEX CONCURRENTLY IF NOT EXISTS issue_ingest_tokens_expiry_idx ON issue_ingest_tokens (expires_at, id);
RESET statement_timeout;
RESET lock_timeout;
-- +goose Down
SET lock_timeout = '5s';
DROP INDEX CONCURRENTLY IF EXISTS issue_ingest_tokens_expiry_idx;
DROP INDEX CONCURRENTLY IF EXISTS issue_events_global_retention_idx;
RESET lock_timeout;
