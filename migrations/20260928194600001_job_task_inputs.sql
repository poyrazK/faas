-- +goose Up
ALTER TABLE job_tasks
    ADD COLUMN IF NOT EXISTS input_id text,
    ADD COLUMN IF NOT EXISTS input_ref text;
-- +goose StatementBegin
DO $$ BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'job_tasks'::regclass AND conname = 'job_tasks_input_binding_check') THEN
        ALTER TABLE job_tasks ADD CONSTRAINT job_tasks_input_binding_check CHECK (
        (input_id IS NULL AND input_ref IS NULL) OR
        (input_id IS NOT NULL AND input_ref IS NOT NULL
         AND length(input_id) BETWEEN 1 AND 128
         AND length(input_ref) BETWEEN 1 AND 2048)
        );
    END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE job_runs
    ADD COLUMN IF NOT EXISTS input_manifest_version int NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS input_digest text;
-- +goose StatementBegin
DO $$ BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'job_runs'::regclass AND conname = 'job_runs_input_manifest_check') THEN
        ALTER TABLE job_runs ADD CONSTRAINT job_runs_input_manifest_check CHECK (
        (input_manifest_version = 0 AND input_digest IS NULL) OR
        (input_manifest_version = 1 AND input_digest ~ '^sha256:[0-9a-f]{64}$')
        );
    END IF;
END $$;
-- +goose StatementEnd
CREATE UNIQUE INDEX IF NOT EXISTS job_tasks_run_input_id_unique
    ON job_tasks (run_id, input_id) WHERE input_id IS NOT NULL;

-- +goose Down
DROP INDEX job_tasks_run_input_id_unique;
ALTER TABLE job_runs
    DROP CONSTRAINT job_runs_input_manifest_check,
    DROP COLUMN input_digest,
    DROP COLUMN input_manifest_version;
ALTER TABLE job_tasks
    DROP CONSTRAINT job_tasks_input_binding_check,
    DROP COLUMN input_ref,
    DROP COLUMN input_id;
