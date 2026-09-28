-- filename: 20260928034921123_platform_tenant_credential_policy.sql

-- +goose Up
-- +goose StatementBegin
-- Platform owners explicitly bound which key scopes downstream tenants may
-- issue, and how many active keys each linked consumer may hold. An empty
-- policy keeps tenant self-service disabled.
CREATE TABLE IF NOT EXISTS platform_tenant_credential_policies (
    tenant_id uuid PRIMARY KEY,
    account_id uuid NOT NULL,
    allowed_scopes text[] NOT NULL DEFAULT '{}',
    max_keys_per_consumer integer NOT NULL DEFAULT 0 CHECK (max_keys_per_consumer BETWEEN 0 AND 100),
    updated_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (account_id, tenant_id)
        REFERENCES platform_tenants(account_id, id) ON DELETE CASCADE,
    CHECK (cardinality(allowed_scopes) <= 3),
    CHECK (array_position(allowed_scopes, NULL) IS NULL),
    CHECK (allowed_scopes <@ ARRAY['read', 'write', 'admin']::text[]),
    CHECK ((cardinality(allowed_scopes) = 0) = (max_keys_per_consumer = 0))
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS platform_tenant_credential_policies;
-- +goose StatementEnd
