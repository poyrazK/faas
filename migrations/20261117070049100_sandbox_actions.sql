-- filename: 20261117070049100_sandbox_actions.sql
-- migration-compat: expand

-- +goose Up
-- The new table's foreign keys lock sandbox_sessions, accounts and
-- managed_realtime_endpoints briefly; no existing table changes.
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
-- ADR-20261117065818 §2: a gregale_browser action on a live browser session.
-- apid writes a pending row for each tool call; agentd, which holds the
-- session, sends the session its next pending action and records the end
-- under the session's lease; apid publishes the end into the call's thread
-- and marks it delivered. One running action per session at a time.
CREATE TABLE IF NOT EXISTS sandbox_actions (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id   uuid NOT NULL REFERENCES sandbox_sessions(id) ON DELETE CASCADE,
    account_id   uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    seq          integer NOT NULL CHECK (seq >= 1),
    kind         text NOT NULL CHECK (kind IN ('navigate', 'click', 'type', 'press', 'scroll', 'back', 'snapshot')),
    args         jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(args) = 'object' AND octet_length(args::text) <= 8192),
    endpoint_id  uuid NOT NULL REFERENCES managed_realtime_endpoints(id) ON DELETE CASCADE,
    thread_id    text NOT NULL CHECK (char_length(thread_id) BETWEEN 1 AND 64),
    tool_call_id text NOT NULL CHECK (char_length(tool_call_id) BETWEEN 1 AND 512),
    issued_at    timestamptz NOT NULL,
    state        text NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'running', 'done')),
    exit_code    integer,
    output       bytea CHECK (output IS NULL OR octet_length(output) <= 262144),
    created_at   timestamptz NOT NULL DEFAULT now(),
    started_at   timestamptz,
    finished_at  timestamptz,
    delivered_at timestamptz,
    CHECK ((state = 'done') = (finished_at IS NOT NULL)),
    UNIQUE (session_id, seq),
    UNIQUE (endpoint_id, thread_id, tool_call_id)
);
CREATE INDEX IF NOT EXISTS sandbox_actions_account_idx ON sandbox_actions (account_id);
CREATE INDEX IF NOT EXISTS sandbox_actions_open_idx ON sandbox_actions (session_id, seq) WHERE state <> 'done';
CREATE INDEX IF NOT EXISTS sandbox_actions_undelivered_idx ON sandbox_actions (finished_at) WHERE state = 'done' AND delivered_at IS NULL;
COMMENT ON TABLE sandbox_actions IS
    'ADR-20261117065818 gregale_browser actions: written by apid, run and ended by agentd under the session lease.';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS sandbox_actions;
-- +goose StatementEnd
