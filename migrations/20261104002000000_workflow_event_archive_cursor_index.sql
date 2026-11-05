-- migration-compat: expand
-- filename: 20261104002000000_workflow_event_archive_cursor_index.sql
-- +goose NO TRANSACTION

-- +goose Up
-- Support a bounded per-run event cursor without blocking event writers.
-- A failed concurrent build can leave an invalid index with this name. Drop
-- it before rebuilding so a migration retry cannot silently retain it.
SET lock_timeout = '5s';
DROP INDEX CONCURRENTLY IF EXISTS workflow_events_run_id_id_idx;
CREATE INDEX CONCURRENTLY IF NOT EXISTS workflow_events_run_id_id_idx
    ON workflow_events (run_id, id);
RESET lock_timeout;

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS workflow_events_run_id_id_idx;
