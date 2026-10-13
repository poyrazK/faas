-- +goose Up
CREATE TABLE IF NOT EXISTS managed_realtime_inbox_message_expirations (
 endpoint_id uuid NOT NULL,
 channel text NOT NULL CHECK (channel ~ '^[0-9a-f]{64}$'),
 sequence bigint NOT NULL CHECK (sequence > 0),
 ttl_seconds integer NOT NULL CHECK (ttl_seconds BETWEEN 1 AND 86400),
 expires_at timestamptz NOT NULL CHECK (isfinite(expires_at)),
 PRIMARY KEY (endpoint_id, channel, sequence),
 FOREIGN KEY (endpoint_id, channel, sequence) REFERENCES managed_realtime_inbox_messages(endpoint_id, channel, sequence) ON DELETE CASCADE
);
-- +goose Down
DROP TABLE managed_realtime_inbox_message_expirations;
