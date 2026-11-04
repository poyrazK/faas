-- filename: 20261103093240972_issue_partial_attribution_index.sql
-- migration-compat: expand
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';
-- A failed concurrent build leaves an invalid index with the same name. A
-- migration retry must remove it rather than silently accepting it.
DROP INDEX CONCURRENTLY IF EXISTS issue_events_partial_attribution_idx;
CREATE INDEX CONCURRENTLY IF NOT EXISTS issue_events_partial_attribution_idx
 ON issue_events(attribution_checked_at)
 WHERE verified_consumer_id IS NULL OR verified_platform_tenant_id IS NULL;
RESET lock_timeout;

-- +goose Down
SET lock_timeout = '5s';
DROP INDEX CONCURRENTLY IF EXISTS issue_events_partial_attribution_idx;
RESET lock_timeout;
