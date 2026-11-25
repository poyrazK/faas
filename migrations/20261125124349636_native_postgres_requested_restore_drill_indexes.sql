-- migration-compat: expand
-- +goose NO TRANSACTION
-- +goose Up
SET lock_timeout = '5s';
SET statement_timeout = 0;

DROP INDEX CONCURRENTLY IF EXISTS database_restore_drills_requested_node;
CREATE UNIQUE INDEX CONCURRENTLY database_restore_drills_requested_node ON database_restore_drills (node_id)
    WHERE request_id IS NOT NULL AND result IN ('queued', 'running');
DROP INDEX CONCURRENTLY IF EXISTS database_restore_drills_requested_source;
CREATE UNIQUE INDEX CONCURRENTLY database_restore_drills_requested_source ON database_restore_drills (source_id)
    WHERE request_id IS NOT NULL AND result IN ('queued', 'running');

RESET statement_timeout;
RESET lock_timeout;

-- +goose Down
SET lock_timeout = '5s';
DROP INDEX CONCURRENTLY IF EXISTS database_restore_drills_requested_source;
DROP INDEX CONCURRENTLY IF EXISTS database_restore_drills_requested_node;
RESET lock_timeout;
