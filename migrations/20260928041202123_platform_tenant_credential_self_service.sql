-- filename: 20260928041202123_platform_tenant_credential_self_service.sql

-- +goose Up
-- +goose StatementBegin
-- Tenant self-service credential operations are separately scoped from
-- hostname management and are enforced against the owner's delegation policy.
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
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM platform_tenant_access_tokens
 WHERE scopes <@ ARRAY[
   'platform_tenant:credentials:read',
   'platform_tenant:credentials:manage'
 ]::text[];
UPDATE platform_tenant_access_tokens
   SET scopes = array_remove(array_remove(scopes, 'platform_tenant:credentials:read'),
                             'platform_tenant:credentials:manage')
 WHERE 'platform_tenant:credentials:read' = ANY(scopes)
    OR 'platform_tenant:credentials:manage' = ANY(scopes);
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
