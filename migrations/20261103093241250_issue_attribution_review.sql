-- filename: 20261103093241250_issue_attribution_review.sql
-- migration-compat: expand
-- +goose NO TRANSACTION
-- New writers mark observations reviewed. Old writers keep the default so the
-- bounded review continues safely during a rolling deployment.
-- +goose Up
SET lock_timeout = '5s';
ALTER TABLE issue_events ADD COLUMN IF NOT EXISTS attribution_version integer NOT NULL DEFAULT 1;
DROP INDEX CONCURRENTLY IF EXISTS issue_events_attribution_review_idx;
CREATE INDEX CONCURRENTLY IF NOT EXISTS issue_events_attribution_review_idx ON issue_events (id)
 WHERE attribution_version < 2 AND (verified_consumer_id IS NOT NULL OR verified_platform_tenant_id IS NOT NULL);
RESET lock_timeout;
-- +goose Down
SET lock_timeout = '5s';
DROP INDEX CONCURRENTLY IF EXISTS issue_events_attribution_review_idx;
ALTER TABLE issue_events DROP COLUMN IF EXISTS attribution_version;
RESET lock_timeout;
