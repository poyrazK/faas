-- filename: 20261010113637885_app_task_attach_sessions.sql

-- +goose Up
-- +goose StatementBegin
-- ADR-958: interactive app tasks. A row exists exactly for tasks admitted as
-- interactive; it is inserted in the same statement as the task, so schedd
-- never claims an interactive task without seeing it. Only the SHA-256
-- digest of the one-time attach token is stored. node_id is recorded by
-- schedd once the task VM is running so gateways can reach its vmmd.
CREATE TABLE IF NOT EXISTS app_task_attach_sessions (
    task_id uuid PRIMARY KEY REFERENCES app_tasks(id) ON DELETE CASCADE,
    tty boolean NOT NULL DEFAULT false,
    attach_token_sha256 bytea NOT NULL,
    node_id text,
    node_recorded_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT app_task_attach_sessions_token_chk CHECK (octet_length(attach_token_sha256) = 32),
    CONSTRAINT app_task_attach_sessions_node_chk CHECK (
        (node_id IS NULL AND node_recorded_at IS NULL)
        OR (octet_length(node_id) BETWEEN 1 AND 255 AND node_recorded_at IS NOT NULL)
    )
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS app_task_attach_sessions;
-- +goose StatementEnd
