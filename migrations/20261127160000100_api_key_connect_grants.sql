-- filename: 20261127160000100_api_key_connect_grants.sql
-- migration-compat: expand

-- +goose Up
-- The new table's foreign key locks api_keys briefly.
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
-- ADR-20261127160000: a hosted MCP sign-in whose person granted deploy access
-- on the Connect screen. That grant is the person's approval of the key's
-- deploys, promotions, checked rollbacks, restarts and undo; granted_by is the
-- account that approved the sign-in. apid is the only writer.
CREATE TABLE IF NOT EXISTS api_key_connect_grants (
    key_id     uuid PRIMARY KEY REFERENCES api_keys(id) ON DELETE CASCADE,
    granted_by uuid NOT NULL,
    granted_at timestamptz NOT NULL DEFAULT now()
);
-- +goose StatementEnd
COMMENT ON TABLE api_key_connect_grants IS
    'ADR-20261127160000 the Connect consent that approves a hosted MCP key''s deploys; apid is the only writer.';

-- +goose Down
DROP TABLE IF EXISTS api_key_connect_grants;
