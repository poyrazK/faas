-- +goose Up
ALTER TABLE managed_realtime_consumer_groups ADD COLUMN IF NOT EXISTS message_filter jsonb NOT NULL DEFAULT '{"event_types":[],"metadata":{}}'::jsonb
 CHECK (jsonb_typeof(message_filter)='object' AND message_filter ? 'event_types' AND message_filter ? 'metadata'
 AND jsonb_typeof(message_filter->'event_types')='array' AND jsonb_array_length(message_filter->'event_types')<=32 AND jsonb_typeof(message_filter->'metadata')='object');
ALTER TABLE managed_realtime_consumer_groups ADD COLUMN IF NOT EXISTS filtered_messages bigint NOT NULL DEFAULT 0 CHECK (filtered_messages>=0);
-- +goose Down
ALTER TABLE managed_realtime_consumer_groups DROP COLUMN message_filter,DROP COLUMN filtered_messages;
-- Advanced assignment cursors cannot restore previously skipped history.
