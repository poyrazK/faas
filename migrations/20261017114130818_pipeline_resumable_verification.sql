-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE data_pipelines ADD COLUMN IF NOT EXISTS verification_progress jsonb NOT NULL DEFAULT '{}' CHECK(jsonb_typeof(verification_progress)='object' AND octet_length(verification_progress::text)<=4194304);
-- +goose Down
SET LOCAL lock_timeout = '5s';
ALTER TABLE data_pipelines DROP COLUMN verification_progress;
