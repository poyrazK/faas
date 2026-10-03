-- +goose Up
ALTER TABLE job_runs
    ADD COLUMN IF NOT EXISTS execution_class text NOT NULL DEFAULT 'standard',
    ADD COLUMN IF NOT EXISTS eligible_at timestamptz,
    ADD COLUMN IF NOT EXISTS latest_start_at timestamptz;
-- +goose StatementBegin
DO $$ BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'job_runs'::regclass AND conname = 'job_runs_execution_window_check') THEN
        ALTER TABLE job_runs ADD CONSTRAINT job_runs_execution_window_check CHECK (
        (execution_class = 'standard' AND eligible_at IS NULL AND latest_start_at IS NULL)
        OR (execution_class = 'flexible' AND eligible_at IS NOT NULL
            AND latest_start_at IS NOT NULL AND latest_start_at > eligible_at)
        );
    END IF;
END $$;
-- +goose StatementEnd
CREATE INDEX IF NOT EXISTS job_runs_flexible_expiry_idx ON job_runs (latest_start_at, id)
    WHERE execution_class = 'flexible' AND aggregate_status IN ('queued', 'running');

-- +goose Down
DROP INDEX job_runs_flexible_expiry_idx;
ALTER TABLE job_runs
    DROP CONSTRAINT job_runs_execution_window_check,
    DROP COLUMN latest_start_at,
    DROP COLUMN eligible_at,
    DROP COLUMN execution_class;
