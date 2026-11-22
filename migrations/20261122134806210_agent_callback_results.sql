-- filename: 20261122134806210_agent_callback_results.sql
-- migration-compat: expand

-- +goose Up
-- The foreign keys lock accounts and managed_realtime_endpoints briefly.
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
-- ADR-20261030125501 §3 amendment 2026-11-22: a stateless agent's tool
-- result larger than one inbox message (AgentCallbackPayloadMaxBytes) is kept
-- in the artifact store under
-- agents/callback-results/<account>/<endpoint>/<id>, and the inbox message
-- carries a preview and the result's URL. apid writes one row per posted
-- message, so a retried post finds its result; the endpoint's live bytes are
-- bounded by AgentCallbackResultsPerEndpointMaxBytes. Rows and objects are
-- deleted once expires_at passes. A new table, so older apid keeps working
-- while this lands.
CREATE TABLE IF NOT EXISTS agent_callback_results (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id  uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    endpoint_id uuid NOT NULL REFERENCES managed_realtime_endpoints(id) ON DELETE CASCADE,
    thread_id   text NOT NULL CHECK (char_length(thread_id) BETWEEN 1 AND 64),
    message_id  text NOT NULL CHECK (char_length(message_id) BETWEEN 1 AND 54),
    sha256      text NOT NULL CHECK (sha256 ~ '^[0-9a-f]{64}$'),
    size_bytes  integer NOT NULL CHECK (size_bytes BETWEEN 1 AND 1048576),
    expires_at  timestamptz NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    CHECK (expires_at > created_at),
    UNIQUE (endpoint_id, thread_id, message_id)
);
CREATE INDEX IF NOT EXISTS agent_callback_results_endpoint_idx ON agent_callback_results (endpoint_id, expires_at);
CREATE INDEX IF NOT EXISTS agent_callback_results_expiry_idx ON agent_callback_results (expires_at);
COMMENT ON TABLE agent_callback_results IS
    'ADR-20261030125501 stored stateless-agent tool results written by apid: the body lives in the artifact store until expires_at.';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS agent_callback_results;
-- +goose StatementEnd
