-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';

ALTER TABLE database_restore_drills
    ADD COLUMN request_id uuid,
    ADD COLUMN pending_outcome text,
    ADD CONSTRAINT database_restore_drills_pending_outcome_check
        CHECK (pending_outcome IN ('ok', 'failed', 'skipped')) NOT VALID,
    ADD CONSTRAINT database_restore_drills_finished_v2
        CHECK ((result IN ('queued', 'running')) = (finished_at IS NULL)) NOT VALID,
    ADD CONSTRAINT database_restore_drills_result_check_v2
        CHECK (result IN ('queued', 'running', 'ok', 'failed', 'skipped')) NOT VALID;

-- +goose Down
SET LOCAL lock_timeout = '5s';
ALTER TABLE database_restore_drills
    DROP CONSTRAINT database_restore_drills_finished_v2,
    DROP CONSTRAINT database_restore_drills_result_check_v2,
    DROP COLUMN pending_outcome,
    DROP COLUMN request_id;
