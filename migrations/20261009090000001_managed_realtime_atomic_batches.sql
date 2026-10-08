-- +goose Up
CREATE TABLE managed_realtime_channel_batches (
 endpoint_id uuid NOT NULL,
 channel text NOT NULL,
 batch_id text NOT NULL CHECK(octet_length(batch_id) BETWEEN 1 AND 128),
 payload_hash bytea NOT NULL CHECK(octet_length(payload_hash)=32),
 first_sequence bigint NOT NULL CHECK(first_sequence>=1),
 message_count integer NOT NULL CHECK(message_count BETWEEN 1 AND 32),
 created_at timestamptz NOT NULL,
 PRIMARY KEY(endpoint_id,channel,batch_id),
 FOREIGN KEY(endpoint_id,channel) REFERENCES managed_realtime_channel_heads(endpoint_id,channel) ON DELETE CASCADE
);
CREATE INDEX managed_realtime_channel_batches_cleanup_idx ON managed_realtime_channel_batches(endpoint_id,created_at);
-- +goose Down
DROP TABLE managed_realtime_channel_batches;
