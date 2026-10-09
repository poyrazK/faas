-- +goose Up
ALTER TABLE managed_realtime_channel_reducers
 ADD COLUMN IF NOT EXISTS entity_expirations json NOT NULL DEFAULT '{}'::json
 CHECK (json_typeof(entity_expirations)='object' AND octet_length(entity_expirations::text)<=65536),
 ADD COLUMN IF NOT EXISTS next_expiry timestamptz;
CREATE INDEX IF NOT EXISTS managed_realtime_reducer_next_expiry_idx
 ON managed_realtime_channel_reducers(next_expiry,endpoint_id,channel) WHERE next_expiry IS NOT NULL;
-- +goose Down
DROP INDEX managed_realtime_reducer_next_expiry_idx;
ALTER TABLE managed_realtime_channel_reducers DROP COLUMN next_expiry, DROP COLUMN entity_expirations;
