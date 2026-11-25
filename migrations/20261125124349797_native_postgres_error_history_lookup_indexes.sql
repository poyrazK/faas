-- migration-compat: expand
-- +goose NO TRANSACTION
-- +goose Up
SET lock_timeout = '5s';
SET statement_timeout = 0;

DROP INDEX CONCURRENTLY IF EXISTS native_postgres_error_bindings_idx;
CREATE INDEX CONCURRENTLY native_postgres_error_bindings_idx ON managed_postgres_bindings (database_id, provider_identity_id)
    WHERE state IN ('ready', 'provisioning', 'retiring');
DROP INDEX CONCURRENTLY IF EXISTS native_postgres_error_placements_idx;
CREATE INDEX CONCURRENTLY native_postgres_error_placements_idx ON managed_postgres_databases ((COALESCE(NULLIF(data_resource_id, ''), provider_resource_id)))
    WHERE state = 'ready';

RESET statement_timeout;
RESET lock_timeout;

-- +goose Down
SET lock_timeout = '5s';
DROP INDEX CONCURRENTLY IF EXISTS native_postgres_error_placements_idx;
DROP INDEX CONCURRENTLY IF EXISTS native_postgres_error_bindings_idx;
RESET lock_timeout;
