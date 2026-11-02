-- filename: 20261031215544747_agent_callback_revocations.sql
-- migration-compat: expand

-- +goose Up
-- The foreign keys lock managed_realtime_endpoints and accounts briefly.
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
-- ADR-20261030125501 §3: closing a thread revokes every callback URL issued
-- for it until then. apid refuses a callback whose token was issued at or
-- before its thread's latest revocation; a URL issued later works, so a
-- thread can reopen. A new table, so older apid keeps working while this
-- lands.
CREATE TABLE IF NOT EXISTS agent_callback_revocations (
    endpoint_id uuid NOT NULL REFERENCES managed_realtime_endpoints(id) ON DELETE CASCADE,
    thread_id   text NOT NULL CHECK (char_length(thread_id) BETWEEN 1 AND 64),
    account_id  uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    revoked_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (endpoint_id, thread_id)
);
CREATE INDEX IF NOT EXISTS agent_callback_revocations_account_idx ON agent_callback_revocations (account_id);
COMMENT ON TABLE agent_callback_revocations IS
    'ADR-20261030125501 agent callback revocations written by apid: a thread''s latest revocation time.';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS agent_callback_revocations;
-- +goose StatementEnd
