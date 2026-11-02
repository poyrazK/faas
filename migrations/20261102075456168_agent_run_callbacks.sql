-- filename: 20261102075456168_agent_run_callbacks.sql
-- migration-compat: expand

-- +goose Up
-- The foreign keys lock agent_runs, accounts and managed_realtime_endpoints
-- briefly.
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
-- ADR-20261030125501 §4: gregale_run links the agent run it admits to the
-- stateless agent's thread and tool call. apid writes the link in the
-- admitting transaction, publishes the finished run into the thread as the
-- call's RAP tool result, and marks the link delivered; a thread revoked
-- after issued_at receives nothing. A new table, so older apid and agentd
-- keep working while this lands.
CREATE TABLE IF NOT EXISTS agent_run_callbacks (
    run_id           uuid PRIMARY KEY REFERENCES agent_runs(id) ON DELETE CASCADE,
    account_id       uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    endpoint_id      uuid NOT NULL REFERENCES managed_realtime_endpoints(id) ON DELETE CASCADE,
    thread_id        text NOT NULL CHECK (char_length(thread_id) BETWEEN 1 AND 64),
    tool_call_id     text NOT NULL CHECK (char_length(tool_call_id) BETWEEN 1 AND 512),
    provider_call_id text CHECK (provider_call_id IS NULL OR char_length(provider_call_id) BETWEEN 1 AND 512),
    issued_at        timestamptz NOT NULL,
    delivered_at     timestamptz,
    created_at       timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS agent_run_callbacks_account_idx ON agent_run_callbacks (account_id);
CREATE INDEX IF NOT EXISTS agent_run_callbacks_endpoint_idx ON agent_run_callbacks (endpoint_id);
CREATE INDEX IF NOT EXISTS agent_run_callbacks_undelivered_idx ON agent_run_callbacks (created_at) WHERE delivered_at IS NULL;
COMMENT ON TABLE agent_run_callbacks IS
    'ADR-20261030125501 gregale_run links written by apid: the thread and tool call a finished agent run answers.';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS agent_run_callbacks;
-- +goose StatementEnd
