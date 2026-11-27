-- migration-compat: expand
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';
SET statement_timeout = 0;

-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM pg_index WHERE indexrelid = to_regclass('database_restore_drills_source_started')
            AND NOT indisvalid
    ) THEN
        DROP INDEX database_restore_drills_source_started;
    END IF;
END;
$$;
-- +goose StatementEnd

CREATE INDEX CONCURRENTLY IF NOT EXISTS database_restore_drills_source_started
    ON database_restore_drills (source_id, started_at DESC, drill_id DESC);

RESET statement_timeout;
RESET lock_timeout;
