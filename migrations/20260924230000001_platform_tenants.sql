-- +goose Up
-- A platform customer can span several app consumers and tenant surfaces.
-- Existing rows stay unbound; no legacy identity is inferred from names.
CREATE TABLE platform_tenants (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    external_ref text NOT NULL CHECK (char_length(external_ref) BETWEEN 1 AND 256),
    name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 128),
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'suspended')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (account_id, external_ref),
    UNIQUE (account_id, id)
);

ALTER TABLE api_consumers ADD COLUMN platform_tenant_id uuid;
ALTER TABLE api_consumers ADD CONSTRAINT api_consumers_platform_tenant_fkey
    FOREIGN KEY (account_id, platform_tenant_id)
    REFERENCES platform_tenants(account_id, id) ON DELETE SET NULL (platform_tenant_id);
CREATE INDEX api_consumers_platform_tenant_idx ON api_consumers(platform_tenant_id)
    WHERE platform_tenant_id IS NOT NULL;

ALTER TABLE tenant_surfaces ADD COLUMN platform_tenant_id uuid;
ALTER TABLE tenant_surfaces ADD CONSTRAINT tenant_surfaces_platform_tenant_fkey
    FOREIGN KEY (account_id, platform_tenant_id)
    REFERENCES platform_tenants(account_id, id) ON DELETE SET NULL (platform_tenant_id);
CREATE INDEX tenant_surfaces_platform_tenant_idx ON tenant_surfaces(platform_tenant_id)
    WHERE platform_tenant_id IS NOT NULL;

-- +goose Down
ALTER TABLE tenant_surfaces DROP COLUMN platform_tenant_id;
ALTER TABLE api_consumers DROP COLUMN platform_tenant_id;
DROP TABLE platform_tenants;
