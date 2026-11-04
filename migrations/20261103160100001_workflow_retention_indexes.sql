-- migration-compat: expand
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';
DROP INDEX CONCURRENTLY IF EXISTS workflow_runs_retention_finished_idx;
CREATE INDEX CONCURRENTLY IF NOT EXISTS workflow_runs_retention_finished_idx
    ON workflow_runs (finished_at, id) WHERE finished_at IS NOT NULL;
DROP INDEX CONCURRENTLY IF EXISTS workflow_events_retention_received_idx;
CREATE INDEX CONCURRENTLY IF NOT EXISTS workflow_events_retention_received_idx
    ON workflow_events (received_at, id);
RESET lock_timeout;

-- +goose Down
SET lock_timeout = '5s';
DROP INDEX CONCURRENTLY IF EXISTS workflow_events_retention_received_idx;
DROP INDEX CONCURRENTLY IF EXISTS workflow_runs_retention_finished_idx;
RESET lock_timeout;
