-- filename: 20261010200000000_app_fork_execs.sql

-- +goose Up
-- +goose StatementBegin
-- ADR-732 fork exec. apid records a command for a running fork; the schedd
-- holding the fork's lease runs it through vmmd inside the quarantined fork
-- and records the bounded result. A command runs in the app's working
-- directory and user, without secrets, and never in a serving instance.
CREATE TABLE IF NOT EXISTS app_fork_execs (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    fork_id uuid NOT NULL REFERENCES app_forks(id) ON DELETE CASCADE,
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    requested_by text NOT NULL,
    command text[] NOT NULL,
    command_shell boolean NOT NULL DEFAULT false,
    timeout_seconds integer NOT NULL,
    max_output_bytes integer NOT NULL,
    status text NOT NULL DEFAULT 'queued',
    exit_code integer,
    output_truncated boolean NOT NULL DEFAULT false,
    stdout bytea NOT NULL DEFAULT ''::bytea,
    stderr bytea NOT NULL DEFAULT ''::bytea,
    failure_code text,
    failure_message text,
    created_at timestamptz NOT NULL,
    started_at timestamptz,
    finished_at timestamptz,
    updated_at timestamptz NOT NULL,
    CONSTRAINT app_fork_execs_status_chk CHECK (
        status IN ('queued', 'running', 'succeeded', 'failed', 'timed_out')
    ),
    CONSTRAINT app_fork_execs_requested_by_chk CHECK (octet_length(requested_by) BETWEEN 1 AND 256),
    CONSTRAINT app_fork_execs_command_chk CHECK (
        cardinality(command) BETWEEN 1 AND 64 AND (NOT command_shell OR cardinality(command) = 1)
    ),
    CONSTRAINT app_fork_execs_timeout_chk CHECK (timeout_seconds BETWEEN 1 AND 3600),
    CONSTRAINT app_fork_execs_output_chk CHECK (
        max_output_bytes BETWEEN 1024 AND 16777216
        AND octet_length(stdout) + octet_length(stderr) <= max_output_bytes
    ),
    CONSTRAINT app_fork_execs_failure_chk CHECK (
        (failure_code IS NULL) = (failure_message IS NULL)
        AND (failure_code IS NULL OR octet_length(failure_code) BETWEEN 1 AND 64)
        AND (failure_message IS NULL OR octet_length(failure_message) <= 512)
    ),
    CONSTRAINT app_fork_execs_times_chk CHECK (
        (started_at IS NULL) = (status = 'queued')
        AND (finished_at IS NULL) = (status IN ('queued', 'running'))
        AND updated_at >= created_at
    )
);

CREATE INDEX IF NOT EXISTS app_fork_execs_fork_idx
    ON app_fork_execs (fork_id, created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS app_fork_execs_pending_idx
    ON app_fork_execs (created_at, id) WHERE status IN ('queued', 'running');
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS app_fork_execs;
-- +goose StatementEnd
