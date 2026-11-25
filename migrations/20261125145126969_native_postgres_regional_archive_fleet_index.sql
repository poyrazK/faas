-- migration-compat: expand
-- +goose NO TRANSACTION
-- +goose Up
SET lock_timeout = '5s';
SET statement_timeout = 0;

-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_index WHERE indexrelid = to_regclass('database_nodes_archive_fleet_key')
        AND NOT indisvalid) THEN
        DROP INDEX database_nodes_archive_fleet_key;
    END IF;
END;
$$;
-- +goose StatementEnd

CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS database_nodes_archive_fleet_key ON database_nodes (id, recovery_region);

RESET statement_timeout;
RESET lock_timeout;

-- +goose Down
SET lock_timeout = '5s';
DROP INDEX CONCURRENTLY IF EXISTS database_nodes_archive_fleet_key;
RESET lock_timeout;
