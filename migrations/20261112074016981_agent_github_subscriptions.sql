-- filename: 20261112074016981_agent_github_subscriptions.sql
-- migration-compat: expand

-- +goose Up
-- The foreign keys lock accounts and managed_realtime_endpoints briefly.
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
-- ADR-20261030125501 §4: gregale_github_subscribe subscribes a stateless
-- agent's thread to a repository's gregale.github events until expires_at.
-- apid writes the row when the tool is called, and publishes each matching
-- event githubd forwards into the thread as a RAP subscription event; a
-- thread revoked after issued_at receives nothing. One row per tool call, so
-- a retried invocation finds its subscription. A new table, so older apid
-- keeps working while this lands.
CREATE TABLE IF NOT EXISTS agent_github_subscriptions (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id       uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    endpoint_id      uuid NOT NULL REFERENCES managed_realtime_endpoints(id) ON DELETE CASCADE,
    thread_id        text NOT NULL CHECK (char_length(thread_id) BETWEEN 1 AND 64),
    tool_call_id     text NOT NULL CHECK (char_length(tool_call_id) BETWEEN 1 AND 512),
    repository       text NOT NULL CHECK (char_length(repository) BETWEEN 3 AND 201 AND repository = lower(repository)),
    event_types      text[] NOT NULL CHECK (cardinality(event_types) BETWEEN 1 AND 8),
    issued_at        timestamptz NOT NULL,
    expires_at       timestamptz NOT NULL,
    created_at       timestamptz NOT NULL DEFAULT now(),
    CHECK (expires_at > created_at),
    UNIQUE (endpoint_id, thread_id, tool_call_id)
);
CREATE INDEX IF NOT EXISTS agent_github_subscriptions_repository_idx ON agent_github_subscriptions (account_id, repository, expires_at);
CREATE INDEX IF NOT EXISTS agent_github_subscriptions_endpoint_idx ON agent_github_subscriptions (endpoint_id, expires_at);
COMMENT ON TABLE agent_github_subscriptions IS
    'ADR-20261030125501 gregale_github_subscribe rows written by apid: a thread receives a repository''s gregale.github events until expires_at.';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS agent_github_subscriptions;
-- +goose StatementEnd
