-- +goose Up
ALTER TABLE job_runs ADD COLUMN IF NOT EXISTS command text[];
-- +goose StatementBegin
DO $$ BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'job_runs'::regclass AND conname = 'job_runs_command_shape_check') THEN
        ALTER TABLE job_runs ADD CONSTRAINT job_runs_command_shape_check
            CHECK (command IS NULL OR cardinality(command) BETWEEN 1 AND 64);
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
ALTER TABLE job_runs
    DROP CONSTRAINT job_runs_command_shape_check,
    DROP COLUMN command;
