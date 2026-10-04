-- +goose Up
ALTER TABLE project_environment_qualifications
ADD COLUMN IF NOT EXISTS workload_config_hashes jsonb NOT NULL DEFAULT '{}'::jsonb
CHECK (jsonb_typeof(workload_config_hashes) = 'object');

-- +goose Down
ALTER TABLE project_environment_qualifications DROP COLUMN workload_config_hashes;
