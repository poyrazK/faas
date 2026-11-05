-- migration-compat: expand
-- +goose NO TRANSACTION
-- +goose Up
SET statement_timeout = 0;
SET lock_timeout = '5s';
DROP INDEX CONCURRENTLY IF EXISTS managed_postgres_overage_pending_recovery_idx;
CREATE INDEX CONCURRENTLY managed_postgres_overage_pending_recovery_idx
 ON managed_postgres_overage_pending(provider, period_start, subject_id);

RESET statement_timeout;

-- +goose Down
SET statement_timeout = 0;
SET lock_timeout = '5s';
DROP INDEX CONCURRENTLY IF EXISTS managed_postgres_overage_pending_recovery_idx;
RESET statement_timeout;
