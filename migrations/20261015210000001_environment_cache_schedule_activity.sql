-- +goose Up
CREATE TABLE environment_cache_schedule_event_counters (
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 project_id uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
 environment_id uuid NOT NULL REFERENCES project_environments(id) ON DELETE CASCADE,
 last_sequence bigint NOT NULL CHECK (last_sequence>0 AND last_sequence<=9007199254740991),
 PRIMARY KEY(app_id,environment_id)
);
CREATE TABLE environment_cache_schedule_events (
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 project_id uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
 environment_id uuid NOT NULL REFERENCES project_environments(id) ON DELETE CASCADE,
 scope text NOT NULL CHECK (length(scope) BETWEEN 1 AND 63),
 sequence bigint NOT NULL CHECK (sequence>0 AND sequence<=9007199254740991),
 payload jsonb NOT NULL CHECK (jsonb_typeof(payload)='object'),
 occurred_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(app_id,environment_id,sequence)
);
-- +goose Down
DROP TABLE environment_cache_schedule_events;
DROP TABLE environment_cache_schedule_event_counters;
