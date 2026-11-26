-- migration-compat: expand
-- +goose NO TRANSACTION
-- +goose Up
SET lock_timeout = '5s';
SET statement_timeout = 0;

-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_index WHERE indexrelid = to_regclass('native_postgres_error_bindings_idx')
        AND NOT indisvalid) THEN
        DROP INDEX native_postgres_error_bindings_idx;
    END IF;
END;
$$;
-- +goose StatementEnd

CREATE INDEX CONCURRENTLY IF NOT EXISTS native_postgres_error_bindings_idx ON managed_postgres_bindings (database_id, provider_identity_id)
    WHERE state IN ('ready', 'provisioning', 'retiring');
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_index WHERE indexrelid = to_regclass('native_postgres_error_placements_idx')
        AND NOT indisvalid) THEN
        DROP INDEX native_postgres_error_placements_idx;
    END IF;
END;
$$;
-- +goose StatementEnd

CREATE INDEX CONCURRENTLY IF NOT EXISTS native_postgres_error_placements_idx ON managed_postgres_databases ((COALESCE(NULLIF(data_resource_id, ''), provider_resource_id)))
    WHERE state = 'ready';

RESET statement_timeout;
RESET lock_timeout;

-- +goose Down
SET lock_timeout = '5s';
DROP INDEX CONCURRENTLY IF EXISTS native_postgres_error_placements_idx;
DROP INDEX CONCURRENTLY IF EXISTS native_postgres_error_bindings_idx;
RESET lock_timeout;
