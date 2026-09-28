-- +goose Up
ALTER TABLE job_tasks
    ADD COLUMN output_manifest jsonb,
    ADD CONSTRAINT job_tasks_output_manifest_check CHECK (
        output_manifest IS NULL OR
        (status = 'succeeded' AND jsonb_typeof(output_manifest) = 'object'
         AND output_manifest->>'version' = '1'
         AND jsonb_typeof(output_manifest->'artifacts') = 'array')
    );

-- +goose Down
ALTER TABLE job_tasks
    DROP CONSTRAINT job_tasks_output_manifest_check,
    DROP COLUMN output_manifest;
