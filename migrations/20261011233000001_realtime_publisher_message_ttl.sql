-- +goose Up
ALTER TABLE managed_realtime_publisher_credentials ADD COLUMN IF NOT EXISTS max_message_ttl_seconds integer NOT NULL DEFAULT 0 CHECK (max_message_ttl_seconds BETWEEN 0 AND 86400);
-- +goose Down
ALTER TABLE managed_realtime_publisher_credentials DROP COLUMN max_message_ttl_seconds;
