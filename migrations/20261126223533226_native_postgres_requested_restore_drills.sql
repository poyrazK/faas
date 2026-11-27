-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';

ALTER TABLE database_restore_drills
    ADD COLUMN IF NOT EXISTS request_id uuid,
    ADD COLUMN IF NOT EXISTS pending_outcome text;

-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'database_restore_drills'::regclass
        AND conname = 'database_restore_drills_pending_outcome_check') THEN
        ALTER TABLE database_restore_drills ADD CONSTRAINT database_restore_drills_pending_outcome_check
            CHECK (pending_outcome IN ('ok', 'failed', 'skipped')) NOT VALID;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'database_restore_drills'::regclass
        AND conname = 'database_restore_drills_finished_v2') THEN
        ALTER TABLE database_restore_drills ADD CONSTRAINT database_restore_drills_finished_v2
            CHECK ((result IN ('queued', 'running')) = (finished_at IS NULL)) NOT VALID;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'database_restore_drills'::regclass
        AND conname = 'database_restore_drills_result_check_v2') THEN
        ALTER TABLE database_restore_drills ADD CONSTRAINT database_restore_drills_result_check_v2
            CHECK (result IN ('queued', 'running', 'ok', 'failed', 'skipped')) NOT VALID;
    END IF;
END;
$$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
ALTER TABLE database_restore_drills
    DROP CONSTRAINT database_restore_drills_finished_v2,
    DROP CONSTRAINT database_restore_drills_result_check_v2,
    DROP COLUMN pending_outcome,
    DROP COLUMN request_id;
