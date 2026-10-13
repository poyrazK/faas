-- +goose Up
ALTER TABLE managed_realtime_consumer_groups ADD COLUMN IF NOT EXISTS ordering_metadata_key text NOT NULL DEFAULT '' CHECK (octet_length(ordering_metadata_key)<=64 AND ordering_metadata_key ~ '^[a-z0-9_.-]*$');
ALTER TABLE managed_realtime_consumer_groups ADD COLUMN IF NOT EXISTS dead_letter_policy text NOT NULL DEFAULT 'block' CHECK (dead_letter_policy IN ('block','skip'));
ALTER TABLE managed_realtime_consumer_group_messages ADD COLUMN IF NOT EXISTS ordering_key text COLLATE "C" NOT NULL DEFAULT '' CHECK (octet_length(ordering_key)<=128);
ALTER TABLE managed_realtime_consumer_group_messages ADD COLUMN IF NOT EXISTS ordered boolean NOT NULL DEFAULT false;
CREATE INDEX IF NOT EXISTS realtime_group_ordering_predecessor ON managed_realtime_consumer_group_messages(group_id,ordering_key,sequence);
CREATE UNIQUE INDEX IF NOT EXISTS realtime_group_ordering_active ON managed_realtime_consumer_group_messages(group_id,ordering_key) WHERE ordered AND status='leased';
-- +goose Down
DROP INDEX realtime_group_ordering_active;
DROP INDEX realtime_group_ordering_predecessor;
ALTER TABLE managed_realtime_consumer_group_messages DROP COLUMN ordering_key,DROP COLUMN ordered;
ALTER TABLE managed_realtime_consumer_groups DROP COLUMN ordering_metadata_key,DROP COLUMN dead_letter_policy;
