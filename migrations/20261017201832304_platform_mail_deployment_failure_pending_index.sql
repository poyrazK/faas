-- filename: 20261017201832304_platform_mail_deployment_failure_pending_index.sql
-- Build without queuing behind ordinary deployment writes. Replacing an
-- interrupted build also removes the invalid index left by PostgreSQL.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';
DROP INDEX CONCURRENTLY IF EXISTS deployments_mail_failures_pending_idx;
CREATE INDEX CONCURRENTLY IF NOT EXISTS deployments_mail_failures_pending_idx ON deployments(mail_failed_at,id)
    WHERE status='failed' AND deleted_at IS NULL AND mail_failed_at IS NOT NULL;
RESET lock_timeout;

-- +goose Down
SET lock_timeout = '5s';
DROP INDEX CONCURRENTLY IF EXISTS deployments_mail_failures_pending_idx;
RESET lock_timeout;
