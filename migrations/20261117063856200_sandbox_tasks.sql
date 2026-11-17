-- filename: 20261117063856200_sandbox_tasks.sql
-- migration-compat: expand

-- +goose Up
-- The columns lock sandbox_sessions briefly and rewrite no rows; the new
-- table's foreign keys lock sandbox_sessions, accounts and
-- managed_realtime_endpoints briefly.
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
-- ADR-20261116182615 §3: a sandbox task is a session that runs one platform
-- command and ends. apid records the task; agentd records its exit code and
-- output as it finishes the session. Nullable, so older binaries keep
-- working.
ALTER TABLE sandbox_sessions
    ADD COLUMN IF NOT EXISTS task_kind text CHECK (task_kind IS NULL OR task_kind IN ('browse')),
    ADD COLUMN IF NOT EXISTS task_url text CHECK (task_url IS NULL OR char_length(task_url) BETWEEN 1 AND 2048),
    ADD COLUMN IF NOT EXISTS task_max_chars integer CHECK (task_max_chars IS NULL OR task_max_chars BETWEEN 1 AND 200000),
    ADD COLUMN IF NOT EXISTS task_exit_code integer,
    ADD COLUMN IF NOT EXISTS task_output bytea CHECK (task_output IS NULL OR octet_length(task_output) <= 262144);
ALTER TABLE sandbox_sessions DROP CONSTRAINT IF EXISTS sandbox_sessions_task_check;
ALTER TABLE sandbox_sessions ADD CONSTRAINT sandbox_sessions_task_check
    CHECK ((task_kind IS NULL) = (task_url IS NULL) AND (task_kind IS NULL) = (task_max_chars IS NULL)) NOT VALID;
COMMENT ON COLUMN sandbox_sessions.task_kind IS
    'ADR-20261116182615: the sandbox task the session runs (browse), or NULL for an ordinary session.';
COMMENT ON COLUMN sandbox_sessions.task_output IS
    'ADR-20261116182615: the task command''s standard output, capped at SandboxTaskOutputMaxBytes.';

-- gregale_browse links the task it admits to the stateless agent's thread
-- and tool call, as gregale_run does with agent_run_callbacks. One task per
-- call, so a retried invocation finds its task.
CREATE TABLE IF NOT EXISTS agent_sandbox_task_calls (
    sandbox_id   uuid PRIMARY KEY REFERENCES sandbox_sessions(id) ON DELETE CASCADE,
    account_id   uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    endpoint_id  uuid NOT NULL REFERENCES managed_realtime_endpoints(id) ON DELETE CASCADE,
    thread_id    text NOT NULL CHECK (char_length(thread_id) BETWEEN 1 AND 64),
    tool_call_id text NOT NULL CHECK (char_length(tool_call_id) BETWEEN 1 AND 512),
    issued_at    timestamptz NOT NULL,
    delivered_at timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now(),
    UNIQUE (endpoint_id, thread_id, tool_call_id)
);
CREATE INDEX IF NOT EXISTS agent_sandbox_task_calls_account_idx ON agent_sandbox_task_calls (account_id);
CREATE INDEX IF NOT EXISTS agent_sandbox_task_calls_undelivered_idx ON agent_sandbox_task_calls (created_at) WHERE delivered_at IS NULL;
COMMENT ON TABLE agent_sandbox_task_calls IS
    'ADR-20261116182615 gregale_browse links written by apid: the thread and tool call a finished sandbox task answers.';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS agent_sandbox_task_calls;
ALTER TABLE sandbox_sessions DROP CONSTRAINT IF EXISTS sandbox_sessions_task_check;
ALTER TABLE sandbox_sessions
    DROP COLUMN IF EXISTS task_output,
    DROP COLUMN IF EXISTS task_exit_code,
    DROP COLUMN IF EXISTS task_max_chars,
    DROP COLUMN IF EXISTS task_url,
    DROP COLUMN IF EXISTS task_kind;
-- +goose StatementEnd
