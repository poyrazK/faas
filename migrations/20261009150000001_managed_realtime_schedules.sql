-- +goose Up
CREATE TABLE managed_realtime_schedules (
 endpoint_id uuid NOT NULL REFERENCES managed_realtime_endpoints(id) ON DELETE CASCADE,
 channel text NOT NULL,
 schedule_id text NOT NULL,
 data bytea NOT NULL CHECK(octet_length(data)<=4096),
 is_binary boolean NOT NULL DEFAULT false,
 metadata json NOT NULL DEFAULT '{}'::json,
 deliver_at timestamptz NOT NULL,
 version bigint NOT NULL DEFAULT 1 CHECK(version>=1 AND version<=9007199254740991),
 status text NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','published','canceled','failed')),
 sequence bigint NOT NULL DEFAULT 0 CHECK(sequence>=0),
 last_error text NOT NULL DEFAULT '',
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(endpoint_id,channel,schedule_id)
);
CREATE INDEX managed_realtime_schedule_due_idx ON managed_realtime_schedules(deliver_at,endpoint_id,channel,schedule_id) WHERE status='pending';
-- +goose Down
DROP TABLE managed_realtime_schedules;
