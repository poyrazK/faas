-- filename: 20261031085334892_api_key_agent_clients.sql
-- migration-compat: expand

-- +goose Up
-- The foreign key locks api_keys briefly.
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
-- ADR-20261030133644: the agent client a hosted MCP sign-in's key belongs to.
-- mcp-bridged names it once through apid, which is the only writer; the first
-- write wins. The previous release neither reads nor writes the table.
CREATE TABLE IF NOT EXISTS api_key_agent_clients (
    key_id      uuid PRIMARY KEY REFERENCES api_keys(id) ON DELETE CASCADE,
    account_id  uuid NOT NULL,
    client_name text NOT NULL CHECK (char_length(client_name) BETWEEN 1 AND 80),
    client_site text NOT NULL DEFAULT '' CHECK (char_length(client_site) <= 253),
    created_at  timestamptz NOT NULL DEFAULT now()
);
-- +goose StatementEnd
CREATE INDEX IF NOT EXISTS api_key_agent_clients_account_idx ON api_key_agent_clients (account_id);
COMMENT ON TABLE api_key_agent_clients IS
    'ADR-20261030133644 agent client names for hosted MCP keys; apid is the only writer.';

-- +goose Down
DROP TABLE IF EXISTS api_key_agent_clients;
