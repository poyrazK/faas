-- +goose Up
CREATE TABLE managed_realtime_channel_reducers (
 endpoint_id uuid NOT NULL,
 channel text NOT NULL,
 sequence bigint NOT NULL CHECK(sequence>=0),
 entities json NOT NULL CHECK(json_typeof(entities)='object' AND octet_length(entities::text)<=65536),
 updated_at timestamptz NOT NULL,
 PRIMARY KEY(endpoint_id,channel),
 FOREIGN KEY(endpoint_id,channel) REFERENCES managed_realtime_channel_heads(endpoint_id,channel) ON DELETE CASCADE
);
-- +goose Down
DROP TABLE managed_realtime_channel_reducers;
