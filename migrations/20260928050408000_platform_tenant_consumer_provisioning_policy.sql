-- filename: 20260928050408000_platform_tenant_consumer_provisioning_policy.sql

-- +goose Up
-- +goose StatementBegin
-- Tenant customer creation is off until the platform owner sets an explicit
-- per-tenant cap. The downstream capability is separately scoped.
CREATE TABLE IF NOT EXISTS platform_tenant_consumer_provisioning_policies (
    tenant_id uuid PRIMARY KEY,
    account_id uuid NOT NULL,
    enabled boolean NOT NULL DEFAULT false,
    max_consumers integer NOT NULL DEFAULT 0 CHECK (max_consumers BETWEEN 0 AND 100000),
    updated_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (account_id, tenant_id)
        REFERENCES platform_tenants(account_id, id) ON DELETE CASCADE,
    CHECK ((enabled AND max_consumers > 0) OR (NOT enabled AND max_consumers = 0))
);

ALTER TABLE platform_tenant_access_tokens
  DROP CONSTRAINT IF EXISTS platform_tenant_access_tokens_scopes_check;
ALTER TABLE platform_tenant_access_tokens
  ADD CONSTRAINT platform_tenant_access_tokens_scopes_check CHECK (
    cardinality(scopes) > 0 AND
    scopes <@ ARRAY[
      'platform_tenant:usage:read',
      'platform_tenant:statements:read',
      'platform_tenant:activation:read',
      'platform_tenant:hostnames:manage',
      'platform_tenant:credentials:read',
      'platform_tenant:credentials:manage',
      'platform_tenant:consumers:manage'
    ]::text[]
  );
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM platform_tenant_access_tokens
 WHERE scopes <@ ARRAY['platform_tenant:consumers:manage']::text[];
UPDATE platform_tenant_access_tokens
   SET scopes = array_remove(scopes, 'platform_tenant:consumers:manage')
 WHERE 'platform_tenant:consumers:manage' = ANY(scopes);
ALTER TABLE platform_tenant_access_tokens
  DROP CONSTRAINT IF EXISTS platform_tenant_access_tokens_scopes_check;
ALTER TABLE platform_tenant_access_tokens
  ADD CONSTRAINT platform_tenant_access_tokens_scopes_check CHECK (
    cardinality(scopes) > 0 AND
    scopes <@ ARRAY[
      'platform_tenant:usage:read',
      'platform_tenant:statements:read',
      'platform_tenant:activation:read',
      'platform_tenant:hostnames:manage',
      'platform_tenant:credentials:read',
      'platform_tenant:credentials:manage'
    ]::text[]
  );
DROP TABLE IF EXISTS platform_tenant_consumer_provisioning_policies;
-- +goose StatementEnd
