-- +goose Up
CREATE TABLE event_recovery_history (
 id bigserial PRIMARY KEY,
 job_id uuid NOT NULL REFERENCES event_recovery_jobs(id) ON DELETE CASCADE,
 occurred_at timestamptz NOT NULL,
 action text NOT NULL CHECK (action IN ('created','paused','resumed','rate_changed','cancelled','expired')),
 actor_kind text NOT NULL CHECK (actor_kind IN ('account','api_key','internal','system')),
 actor_id text NOT NULL CHECK (length(actor_id)>0 AND octet_length(actor_id)<=256),
 reason text NOT NULL DEFAULT '' CHECK (octet_length(reason)<=512),
 previous_state text NOT NULL CHECK (previous_state IN ('','running','paused','completed','cancelled')),
 state text NOT NULL CHECK (state IN ('running','paused','completed','cancelled')),
 previous_rate integer NOT NULL CHECK (previous_rate BETWEEN 0 AND 100),
 rate integer NOT NULL CHECK (rate BETWEEN 1 AND 100)
);
CREATE INDEX event_recovery_history_job_idx ON event_recovery_history(job_id,id);

-- +goose Down
DROP TABLE event_recovery_history;
