-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';

ALTER TABLE database_restore_drills
    VALIDATE CONSTRAINT database_restore_drills_pending_outcome_check,
    VALIDATE CONSTRAINT database_restore_drills_finished_v2,
    VALIDATE CONSTRAINT database_restore_drills_result_check_v2;

-- +goose Down
SELECT 1;
