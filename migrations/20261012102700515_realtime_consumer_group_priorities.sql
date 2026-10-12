-- +goose Up
ALTER TABLE managed_realtime_consumer_groups ADD COLUMN IF NOT EXISTS priority_metadata_key text NOT NULL DEFAULT '' CHECK (priority_metadata_key='' OR (octet_length(priority_metadata_key)<=64 AND priority_metadata_key~'^[a-z0-9_.-]+$'));
ALTER TABLE managed_realtime_consumer_groups ADD COLUMN IF NOT EXISTS priority_turn integer NOT NULL DEFAULT 0 CHECK (priority_turn BETWEEN 0 AND 7);
ALTER TABLE managed_realtime_consumer_group_messages ADD COLUMN IF NOT EXISTS priority integer NOT NULL DEFAULT 0 CHECK (priority BETWEEN 0 AND 9);
-- +goose Down
ALTER TABLE managed_realtime_consumer_group_messages DROP COLUMN priority;
ALTER TABLE managed_realtime_consumer_groups DROP COLUMN priority_metadata_key,DROP COLUMN priority_turn;
