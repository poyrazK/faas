-- +goose Up
ALTER TABLE managed_realtime_channel_messages ADD COLUMN metadata jsonb NOT NULL DEFAULT '{}' CHECK(jsonb_typeof(metadata)='object' AND octet_length(metadata::text)<=4096);
ALTER TABLE managed_realtime_inbox_messages ADD COLUMN metadata jsonb NOT NULL DEFAULT '{}' CHECK(jsonb_typeof(metadata)='object' AND octet_length(metadata::text)<=4096);
-- +goose Down
ALTER TABLE managed_realtime_inbox_messages DROP COLUMN metadata;
ALTER TABLE managed_realtime_channel_messages DROP COLUMN metadata;
