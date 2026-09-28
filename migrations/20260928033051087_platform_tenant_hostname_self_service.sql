-- filename: 20260928033051087_platform_tenant_hostname_self_service.sql

-- +goose Up
-- +goose StatementBegin
-- The new token capability can only add a DNS-verified hostname to a
-- pre-linked surface and is enforced against an owner-managed policy.
ALTER TABLE platform_tenant_access_tokens
  DROP CONSTRAINT IF EXISTS platform_tenant_access_tokens_scopes_check;
ALTER TABLE platform_tenant_access_tokens
  ADD CONSTRAINT platform_tenant_access_tokens_scopes_check CHECK (
    cardinality(scopes) > 0 AND
    scopes <@ ARRAY[
      'platform_tenant:usage:read',
      'platform_tenant:statements:read',
      'platform_tenant:activation:read',
      'platform_tenant:hostnames:manage'
    ]::text[]
  );
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Tokens whose only capability is hostname management become unusable after
-- rollback; keep any token that still has a supported read capability.
DELETE FROM platform_tenant_access_tokens
 WHERE scopes = ARRAY['platform_tenant:hostnames:manage']::text[];
UPDATE platform_tenant_access_tokens
   SET scopes = array_remove(scopes, 'platform_tenant:hostnames:manage')
 WHERE 'platform_tenant:hostnames:manage' = ANY(scopes);
ALTER TABLE platform_tenant_access_tokens
  DROP CONSTRAINT IF EXISTS platform_tenant_access_tokens_scopes_check;
ALTER TABLE platform_tenant_access_tokens
  ADD CONSTRAINT platform_tenant_access_tokens_scopes_check CHECK (
    cardinality(scopes) > 0 AND
    scopes <@ ARRAY[
      'platform_tenant:usage:read',
      'platform_tenant:statements:read',
      'platform_tenant:activation:read'
    ]::text[]
  );
-- +goose StatementEnd
