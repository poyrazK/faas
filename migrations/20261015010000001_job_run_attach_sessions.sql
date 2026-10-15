-- filename: 20261015010000001_job_run_attach_sessions.sql

-- +goose Up
-- +goose StatementBegin
-- ADR-1330: interactive image runs. A row exists exactly for runs admitted as
-- interactive; it is inserted in the same transaction as the run, so schedd
-- never claims an interactive task without seeing it. An interactive run has
-- one task. Only the SHA-256 digest of the one-time attach token is stored;
-- vmmd matches it when the client attaches.
CREATE TABLE IF NOT EXISTS job_run_attach_sessions (
    run_id uuid PRIMARY KEY REFERENCES job_runs(id) ON DELETE CASCADE,
    tty boolean NOT NULL DEFAULT false,
    attach_token_sha256 bytea NOT NULL,
    attach_timeout_s integer NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT job_run_attach_sessions_token_chk CHECK (octet_length(attach_token_sha256) = 32),
    CONSTRAINT job_run_attach_sessions_timeout_chk CHECK (attach_timeout_s BETWEEN 1 AND 300)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS job_run_attach_sessions;
-- +goose StatementEnd
