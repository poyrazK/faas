-- filename: 20261105141918783_managed_postgres_rotation_refresh_index.sql
-- migration-compat: expand
-- ADR-20261105081859: discover rotated credentials that lost their refresh.
-- +goose NO TRANSACTION

-- +goose Up
SET statement_timeout = 0;
SET lock_timeout = '5s';
-- An interrupted concurrent build may leave an invalid index of this name.
DROP INDEX CONCURRENTLY IF EXISTS managed_postgres_bindings_refresh_reconcile_idx;
CREATE INDEX CONCURRENTLY managed_postgres_bindings_refresh_reconcile_idx
 ON managed_postgres_bindings (retry_at,id)
 WHERE state='ready' AND access<>'migration'
  AND rotation_previous_generation IS NOT NULL AND NOT rotation_cleanup_ready;

RESET statement_timeout;

-- +goose Down
SET statement_timeout = 0;
SET lock_timeout = '5s';
DROP INDEX CONCURRENTLY IF EXISTS managed_postgres_bindings_refresh_reconcile_idx;
RESET statement_timeout;
