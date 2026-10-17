-- +goose Up
ALTER TABLE data_pipelines ADD COLUMN verification_progress jsonb NOT NULL DEFAULT '{}' CHECK(jsonb_typeof(verification_progress)='object' AND octet_length(verification_progress::text)<=4194304);
-- +goose Down
ALTER TABLE data_pipelines DROP COLUMN verification_progress;
