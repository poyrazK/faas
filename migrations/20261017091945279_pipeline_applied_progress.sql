-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE data_pipelines ADD COLUMN IF NOT EXISTS applied_through_at timestamptz;

-- +goose Down
SET LOCAL lock_timeout = '5s';
ALTER TABLE data_pipelines DROP COLUMN applied_through_at;
