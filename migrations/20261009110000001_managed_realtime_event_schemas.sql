-- +goose Up
CREATE TABLE managed_realtime_event_schemas (
 endpoint_id uuid NOT NULL REFERENCES managed_realtime_endpoints(id) ON DELETE CASCADE,
 channel text NOT NULL CHECK(octet_length(channel) BETWEEN 1 AND 256),
 event_type text NOT NULL CHECK(event_type ~ '^[a-z0-9_.-]{1,64}$'),
 version integer NOT NULL CHECK(version BETWEEN 1 AND 1000000),
 schema json NOT NULL CHECK(octet_length(schema::text)<=16384),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(endpoint_id,channel,event_type,version)
);
-- +goose Down
DROP TABLE managed_realtime_event_schemas;
