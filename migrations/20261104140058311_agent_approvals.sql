-- filename: 20261104140058311_agent_approvals.sql
-- migration-compat: expand

-- +goose Up
-- The new tables' foreign keys lock api_keys briefly; the new column locks org_agent_policies.
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
-- ADR-20261104140058: the categories of actions an agent or mcp-login key must
-- ask a person to approve. No row means every category; an empty array means
-- none (the organization's floor still applies). apid is the only writer.
CREATE TABLE IF NOT EXISTS api_key_approval_categories (
    key_id     uuid PRIMARY KEY REFERENCES api_keys(id) ON DELETE CASCADE,
    categories text[] NOT NULL CHECK (categories <@ ARRAY['delete', 'production', 'credentials', 'billing']::text[]),
    created_at timestamptz NOT NULL DEFAULT now()
);
-- +goose StatementEnd
-- +goose StatementBegin
-- ADR-20261104140058: a request of an agent key waiting for, or holding, a
-- person's decision. One approval allows exactly one matching request.
CREATE TABLE IF NOT EXISTS agent_approval_requests (
    id          uuid PRIMARY KEY,
    key_id      uuid NOT NULL REFERENCES api_keys(id) ON DELETE CASCADE,
    account_id  uuid NOT NULL,
    org_id      uuid NOT NULL,
    category    text NOT NULL CHECK (category IN ('delete', 'production', 'credentials', 'billing')),
    method      text NOT NULL CHECK (method IN ('POST', 'PUT', 'PATCH', 'DELETE')),
    path        text NOT NULL CHECK (length(path) BETWEEN 1 AND 2048),
    body_sha256 bytea CHECK (body_sha256 IS NULL OR length(body_sha256) = 32),
    status      text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'approved', 'denied', 'consumed')),
    decided_by  uuid,
    created_at  timestamptz NOT NULL DEFAULT now(),
    decided_at  timestamptz,
    consumed_at timestamptz,
    expires_at  timestamptz NOT NULL,
    CHECK ((status = 'pending') = (decided_at IS NULL)),
    CHECK ((status = 'consumed') = (consumed_at IS NOT NULL))
);
-- +goose StatementEnd
CREATE INDEX IF NOT EXISTS agent_approval_requests_key_idx ON agent_approval_requests (key_id, created_at DESC);
CREATE INDEX IF NOT EXISTS agent_approval_requests_pending_account_idx ON agent_approval_requests (account_id) WHERE status = 'pending';
CREATE INDEX IF NOT EXISTS agent_approval_requests_pending_org_idx ON agent_approval_requests (org_id) WHERE status = 'pending';
-- +goose StatementBegin
-- ADR-20261104140058: categories every agent acting in the organization must
-- have approved, whatever its connection chose. The previous release ignores it.
ALTER TABLE org_agent_policies
    ADD COLUMN IF NOT EXISTS approval_categories text[] NOT NULL DEFAULT '{}'::text[];
-- +goose StatementEnd
-- +goose StatementBegin
-- NOT VALID: new writes are checked at once; existing rows all hold the
-- default '{}', so a later migration can VALIDATE without finding any.
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'org_agent_policies_approval_categories_check') THEN
        ALTER TABLE org_agent_policies
            ADD CONSTRAINT org_agent_policies_approval_categories_check
            CHECK (approval_categories <@ ARRAY['delete', 'production', 'credentials', 'billing']::text[]) NOT VALID;
    END IF;
END
$$;
-- +goose StatementEnd
COMMENT ON TABLE api_key_approval_categories IS
    'ADR-20261104140058 the action categories an agent key must have a person approve; apid is the only writer.';
COMMENT ON TABLE agent_approval_requests IS
    'ADR-20261104140058 agent requests awaiting or holding a person''s approval; apid is the only writer.';

-- +goose Down
ALTER TABLE org_agent_policies DROP COLUMN IF EXISTS approval_categories;
DROP TABLE IF EXISTS agent_approval_requests;
DROP TABLE IF EXISTS api_key_approval_categories;
