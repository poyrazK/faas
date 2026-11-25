-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';

ALTER TABLE database_restore_drills
    DROP CONSTRAINT IF EXISTS database_restore_drills_finished,
    DROP CONSTRAINT IF EXISTS database_restore_drills_result_check;

-- +goose Down
SET LOCAL lock_timeout = '5s';
UPDATE database_restore_drills SET result = 'running' WHERE result = 'queued';
ALTER TABLE database_restore_drills
    ADD CONSTRAINT database_restore_drills_finished CHECK ((result = 'running') = (finished_at IS NULL)),
    ADD CONSTRAINT database_restore_drills_result_check CHECK (result IN ('running', 'ok', 'failed', 'skipped'));
