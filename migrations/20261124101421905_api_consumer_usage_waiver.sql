-- +goose Up
ALTER TABLE api_consumer_usage_statements ADD COLUMN IF NOT EXISTS waiver jsonb;

-- +goose Down
ALTER TABLE api_consumer_usage_statements DROP COLUMN IF EXISTS waiver;
