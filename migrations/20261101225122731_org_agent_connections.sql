-- filename: 20261101225122731_org_agent_connections.sql
-- migration-compat: expand

-- +goose Up
-- The new column and foreign keys lock cli_auth_codes and orgs briefly.
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
-- ADR-20261101225122: a hosted MCP sign-in acts in the organization its person
-- chose on the approval page; the exchange binds the connection's key to it.
-- NULL keeps today's personal-organization key. The previous release never
-- writes it and ignores it.
ALTER TABLE cli_auth_codes
    ADD COLUMN IF NOT EXISTS org_id uuid REFERENCES orgs(id) ON DELETE CASCADE;
-- +goose StatementEnd
-- +goose StatementBegin
-- ADR-20261101225122: a shared organization's agent policy. No row means
-- agents are allowed with every grantable capability. apid is the only writer.
CREATE TABLE IF NOT EXISTS org_agent_policies (
    org_id         uuid PRIMARY KEY REFERENCES orgs(id) ON DELETE CASCADE,
    agents_allowed boolean NOT NULL DEFAULT true,
    allowed_scopes text[] NOT NULL
        DEFAULT ARRAY['deploy:write', 'secrets:request', 'storage:manage', 'postgres:manage']::text[]
        CHECK (allowed_scopes <@ ARRAY['deploy:write', 'secrets:request', 'storage:manage', 'postgres:manage']::text[]),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    updated_by     uuid REFERENCES accounts(id) ON DELETE SET NULL
);
-- +goose StatementEnd
COMMENT ON TABLE org_agent_policies IS
    'ADR-20261101225122 whether agents may act in an organization and with which capabilities; apid is the only writer.';

-- +goose Down
DROP TABLE IF EXISTS org_agent_policies;
ALTER TABLE cli_auth_codes DROP COLUMN IF EXISTS org_id;
