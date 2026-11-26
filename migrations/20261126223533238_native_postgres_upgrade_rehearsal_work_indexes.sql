-- migration-compat: expand
-- +goose NO TRANSACTION
-- One restore drill or rehearsal may reserve the source and node.
-- Rehearsals stay queued in the base table until cleanup, keeping older
-- schedulers from treating them as ordinary drills.
-- +goose Up
SET lock_timeout = '5s';
SET statement_timeout = 0;

-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_index WHERE indexrelid = to_regclass('database_restore_drills_work_node')
        AND NOT indisvalid) THEN
        DROP INDEX database_restore_drills_work_node;
    END IF;
END;
$$;
-- +goose StatementEnd

CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS database_restore_drills_work_node ON database_restore_drills (node_id)
    WHERE result = 'queued' OR (request_id IS NOT NULL AND result = 'running');
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_index WHERE indexrelid = to_regclass('database_restore_drills_work_source')
        AND NOT indisvalid) THEN
        DROP INDEX database_restore_drills_work_source;
    END IF;
END;
$$;
-- +goose StatementEnd

CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS database_restore_drills_work_source ON database_restore_drills (source_id)
    WHERE result = 'queued' OR (request_id IS NOT NULL AND result = 'running');

RESET statement_timeout;
RESET lock_timeout;

-- +goose Down
SET lock_timeout = '5s';
DROP INDEX CONCURRENTLY IF EXISTS database_restore_drills_work_source;
DROP INDEX CONCURRENTLY IF EXISTS database_restore_drills_work_node;
RESET lock_timeout;
