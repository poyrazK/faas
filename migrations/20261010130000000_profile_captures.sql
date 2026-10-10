-- +goose Up
-- On-demand profile captures (ADR-967). apid queues a capture and emits
-- pg_notify('profile_capture'); schedd claims it, runs it through vmmd and
-- writes the result. capture holds the request and result metadata;
-- profile_capture_data holds each collector's unparsed pprof profile.
CREATE TABLE IF NOT EXISTS profile_captures (
    id uuid PRIMARY KEY,
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    status text NOT NULL CHECK (status IN ('queued', 'capturing', 'ready', 'failed')),
    capture jsonb NOT NULL CHECK (jsonb_typeof(capture) = 'object' AND octet_length(capture::text) <= 16384),
    created_at timestamptz NOT NULL DEFAULT now(),
    claimed_at timestamptz,
    completed_at timestamptz,
    expires_at timestamptz NOT NULL
);
CREATE INDEX IF NOT EXISTS profile_captures_app_created_idx ON profile_captures (app_id, created_at DESC, id);
CREATE INDEX IF NOT EXISTS profile_captures_account_created_idx ON profile_captures (account_id, created_at DESC);
CREATE INDEX IF NOT EXISTS profile_captures_status_created_idx ON profile_captures (status, created_at);
CREATE INDEX IF NOT EXISTS profile_captures_expires_idx ON profile_captures (expires_at);

CREATE TABLE IF NOT EXISTS profile_capture_data (
    capture_id uuid NOT NULL REFERENCES profile_captures(id) ON DELETE CASCADE,
    seq smallint NOT NULL CHECK (seq BETWEEN 0 AND 7),
    kind text NOT NULL CHECK (kind IN ('cpu', 'heap')),
    process_id text NOT NULL CHECK (octet_length(process_id) <= 16),
    profile bytea NOT NULL CHECK (octet_length(profile) BETWEEN 1 AND 1048576),
    PRIMARY KEY (capture_id, seq)
);

-- +goose Down
DROP TABLE profile_capture_data;
DROP TABLE profile_captures;
