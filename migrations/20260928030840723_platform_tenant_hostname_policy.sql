-- filename: 20260928030840723_platform_tenant_hostname_policy.sql

-- +goose Up
-- +goose StatementBegin
-- A platform owner can delegate only explicitly allowed DNS suffixes to a
-- downstream tenant. Empty policy means self-service hostname creation is off.
CREATE TABLE IF NOT EXISTS platform_tenant_hostname_policies (
    tenant_id uuid PRIMARY KEY,
    account_id uuid NOT NULL,
    allowed_suffixes text[] NOT NULL DEFAULT '{}',
    max_hostnames integer NOT NULL DEFAULT 0 CHECK (max_hostnames BETWEEN 0 AND 100),
    updated_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (account_id, tenant_id)
        REFERENCES platform_tenants(account_id, id) ON DELETE CASCADE,
    CHECK (cardinality(allowed_suffixes) <= 32),
    CHECK (array_position(allowed_suffixes, NULL) IS NULL),
    CHECK ((cardinality(allowed_suffixes) = 0) = (max_hostnames = 0))
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS platform_tenant_hostname_policies;
-- +goose StatementEnd
