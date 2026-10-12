-- +goose Up
CREATE TABLE IF NOT EXISTS managed_realtime_replay_jobs (
 id uuid PRIMARY KEY,
 group_id uuid NOT NULL REFERENCES managed_realtime_consumer_groups(id) ON DELETE CASCADE,
 state text NOT NULL CHECK (state IN ('running','paused','completed','cancelled')),
 rate integer NOT NULL CHECK (rate BETWEEN 1 AND 32),
 selection jsonb NOT NULL CHECK(jsonb_typeof(selection)='array' AND jsonb_array_length(selection) BETWEEN 1 AND 128),
 outcomes jsonb NOT NULL DEFAULT '[]' CHECK(jsonb_typeof(outcomes)='array' AND jsonb_array_length(outcomes)<=128),
 error_count bigint NOT NULL DEFAULT 0 CHECK(error_count>=0),
 last_error text NOT NULL DEFAULT '',
 position integer NOT NULL DEFAULT 0 CHECK(position BETWEEN 0 AND 128),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 next_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX IF NOT EXISTS realtime_replay_jobs_due ON managed_realtime_replay_jobs(next_at) WHERE state='running';
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM managed_realtime_replay_jobs WHERE state IN ('running','paused')) THEN
 RAISE EXCEPTION 'Cancel active replay jobs before downgrade'; END IF;
END $$;
-- +goose StatementEnd
DROP TABLE managed_realtime_replay_jobs;
