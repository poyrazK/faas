-- +goose Up
ALTER TABLE data_pipelines ADD COLUMN applied_through_at timestamptz;

-- +goose Down
ALTER TABLE data_pipelines DROP COLUMN applied_through_at;
