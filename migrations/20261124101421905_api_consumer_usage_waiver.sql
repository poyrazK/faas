-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE api_consumer_usage_statements ADD COLUMN IF NOT EXISTS waiver jsonb;

-- +goose Down
SET LOCAL lock_timeout = '5s';
ALTER TABLE api_consumer_usage_statements DROP COLUMN IF EXISTS waiver;
