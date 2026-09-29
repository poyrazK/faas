-- +goose Up
ALTER TABLE job_runs
    ADD COLUMN IF NOT EXISTS failure_policy text NOT NULL DEFAULT 'continue';
-- +goose StatementBegin
DO $$ BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'job_runs'::regclass AND conname = 'job_runs_failure_policy_check') THEN
        ALTER TABLE job_runs ADD CONSTRAINT job_runs_failure_policy_check
            CHECK (failure_policy IN ('continue', 'fail_fast'));
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
ALTER TABLE job_runs
    DROP CONSTRAINT job_runs_failure_policy_check,
    DROP COLUMN failure_policy;
