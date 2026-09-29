-- +goose Up
ALTER TABLE job_tasks
    ADD COLUMN IF NOT EXISTS output_manifest jsonb;
-- +goose StatementBegin
DO $$ BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'job_tasks'::regclass AND conname = 'job_tasks_output_manifest_check') THEN
        ALTER TABLE job_tasks ADD CONSTRAINT job_tasks_output_manifest_check CHECK (
        output_manifest IS NULL OR
        (status = 'succeeded' AND jsonb_typeof(output_manifest) = 'object'
         AND output_manifest->>'version' = '1'
         AND jsonb_typeof(output_manifest->'artifacts') = 'array')
        );
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
ALTER TABLE job_tasks
    DROP CONSTRAINT job_tasks_output_manifest_check,
    DROP COLUMN output_manifest;
