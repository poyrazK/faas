-- filename: 20261103093241044_issue_attribution_rechecks.sql
-- migration-compat: expand
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';
DROP INDEX CONCURRENTLY IF EXISTS issue_events_pending_attribution_idx;
CREATE INDEX CONCURRENTLY IF NOT EXISTS issue_events_pending_attribution_idx
 ON issue_events(attribution_checked_at)
 WHERE attribution_checked_at <> 'infinity'::timestamptz;
RESET lock_timeout;

-- +goose Down
SET lock_timeout = '5s';
DROP INDEX CONCURRENTLY IF EXISTS issue_events_pending_attribution_idx;
RESET lock_timeout;
