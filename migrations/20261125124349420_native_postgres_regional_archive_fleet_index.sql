-- migration-compat: expand
-- +goose NO TRANSACTION
-- +goose Up
SET lock_timeout = '5s';
SET statement_timeout = 0;

DROP INDEX CONCURRENTLY IF EXISTS database_nodes_archive_fleet_key;
CREATE UNIQUE INDEX CONCURRENTLY database_nodes_archive_fleet_key ON database_nodes (id, recovery_region);

RESET statement_timeout;
RESET lock_timeout;

-- +goose Down
SET lock_timeout = '5s';
DROP INDEX CONCURRENTLY IF EXISTS database_nodes_archive_fleet_key;
RESET lock_timeout;
