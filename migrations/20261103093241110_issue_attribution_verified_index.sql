-- filename: 20261103093241110_issue_attribution_verified_index.sql
-- migration-compat: expand
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';
DROP INDEX CONCURRENTLY IF EXISTS issue_events_recent_verified_idx;
CREATE INDEX CONCURRENTLY IF NOT EXISTS issue_events_recent_verified_idx
 ON issue_events(received_at, attribution_checked_at)
 WHERE verified_consumer_id IS NOT NULL AND verified_platform_tenant_id IS NOT NULL
 AND attribution_checked_at <> 'infinity'::timestamptz;
RESET lock_timeout;

-- +goose Down
SET lock_timeout = '5s';
DROP INDEX CONCURRENTLY IF EXISTS issue_events_recent_verified_idx;
RESET lock_timeout;
