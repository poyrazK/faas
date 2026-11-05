-- filename: 20261104085712418_agent_project_limits.sql
-- migration-compat: expand

-- +goose Up
-- The new table's and column's foreign keys lock api_keys and cli_auth_codes briefly.
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
-- ADR-20261104085712: an agent or mcp-login key limited to projects of its
-- organization. A row means the key is limited; no row means it is not.
-- Deleting a project leaves its id here, where it matches nothing, so a limit
-- never widens. apid is the only writer; the previous release ignores it.
CREATE TABLE IF NOT EXISTS api_key_project_limits (
    key_id      uuid PRIMARY KEY REFERENCES api_keys(id) ON DELETE CASCADE,
    project_ids uuid[] NOT NULL CHECK (cardinality(project_ids) BETWEEN 1 AND 100),
    created_at  timestamptz NOT NULL DEFAULT now()
);
-- +goose StatementEnd
-- +goose StatementBegin
-- ADR-20261104085712: the projects a hosted MCP sign-in was limited to on the
-- approval page. NULL means every project.
ALTER TABLE cli_auth_codes
    ADD COLUMN IF NOT EXISTS project_ids uuid[];
-- +goose StatementEnd
COMMENT ON TABLE api_key_project_limits IS
    'ADR-20261104085712 the projects an agent or mcp-login key is limited to; apid is the only writer.';

-- +goose Down
ALTER TABLE cli_auth_codes DROP COLUMN IF EXISTS project_ids;
DROP TABLE IF EXISTS api_key_project_limits;
