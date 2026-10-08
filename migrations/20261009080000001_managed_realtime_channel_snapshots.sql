-- +goose Up
CREATE TABLE managed_realtime_channel_snapshots (
 endpoint_id uuid NOT NULL,
 channel text NOT NULL,
 sequence bigint NOT NULL CHECK(sequence>=0),
 data bytea NOT NULL CHECK(octet_length(data)<=65536),
 is_binary boolean NOT NULL DEFAULT false,
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 expires_at timestamptz NOT NULL DEFAULT clock_timestamp()+interval '24 hours',
 PRIMARY KEY(endpoint_id,channel),
 FOREIGN KEY(endpoint_id,channel) REFERENCES managed_realtime_channel_heads(endpoint_id,channel) ON DELETE CASCADE
);
-- +goose Down
DROP TABLE managed_realtime_channel_snapshots;
