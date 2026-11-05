-- migration-compat: expand
-- +goose NO TRANSACTION
-- +goose Up
SET lock_timeout = '5s';
DROP INDEX CONCURRENTLY IF EXISTS managed_postgres_overage_pending_recovery_idx;
CREATE INDEX CONCURRENTLY managed_postgres_overage_pending_recovery_idx
 ON managed_postgres_overage_pending(provider, period_start, subject_id);

-- +goose Down
SET lock_timeout = '5s';
DROP INDEX CONCURRENTLY IF EXISTS managed_postgres_overage_pending_recovery_idx;
