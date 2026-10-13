-- +goose Up
CREATE TABLE IF NOT EXISTS managed_realtime_outbox_observations (
 source_id uuid NOT NULL REFERENCES commit_sources(id) ON DELETE CASCADE,
 event_id uuid NOT NULL,
 endpoint_id text NOT NULL,
 channel text NOT NULL,
 attempts integer NOT NULL CHECK(attempts>=0),
 blocked_code text,
 created_at timestamptz NOT NULL,
 observed_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(source_id,event_id)
);
-- +goose Down
DROP TABLE managed_realtime_outbox_observations;
