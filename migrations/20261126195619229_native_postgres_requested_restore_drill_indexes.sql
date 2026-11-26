-- migration-compat: expand
-- +goose NO TRANSACTION
-- +goose Up
SET lock_timeout = '5s';
SET statement_timeout = 0;

-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_index WHERE indexrelid = to_regclass('database_restore_drills_requested_node')
        AND NOT indisvalid) THEN
        DROP INDEX database_restore_drills_requested_node;
    END IF;
END;
$$;
-- +goose StatementEnd

CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS database_restore_drills_requested_node ON database_restore_drills (node_id)
    WHERE request_id IS NOT NULL AND result IN ('queued', 'running');
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_index WHERE indexrelid = to_regclass('database_restore_drills_requested_source')
        AND NOT indisvalid) THEN
        DROP INDEX database_restore_drills_requested_source;
    END IF;
END;
$$;
-- +goose StatementEnd

CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS database_restore_drills_requested_source ON database_restore_drills (source_id)
    WHERE request_id IS NOT NULL AND result IN ('queued', 'running');

RESET statement_timeout;
RESET lock_timeout;

-- +goose Down
SET lock_timeout = '5s';
DROP INDEX CONCURRENTLY IF EXISTS database_restore_drills_requested_source;
DROP INDEX CONCURRENTLY IF EXISTS database_restore_drills_requested_node;
RESET lock_timeout;
