-- filename: 20260928021020711_platform_tenant_self_activation.sql

-- +goose Up
-- +goose StatementBegin
-- Downstream tenant bearers may read only the bounded activation snapshot for
-- the tenant to which the bearer is bound. Keep this capability in the
-- existing closed scope vocabulary stored with each token.
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

-- +goose Down
-- +goose StatementBegin
-- Tokens whose only capability is activation are invalidated on rollback;
-- retain other tokens while removing only the rolled-back scope.
DELETE FROM platform_tenant_access_tokens
 WHERE scopes = ARRAY['platform_tenant:activation:read']::text[];
UPDATE platform_tenant_access_tokens
   SET scopes = array_remove(scopes, 'platform_tenant:activation:read')
 WHERE 'platform_tenant:activation:read' = ANY(scopes);
ALTER TABLE platform_tenant_access_tokens
  DROP CONSTRAINT IF EXISTS platform_tenant_access_tokens_scopes_check;
ALTER TABLE platform_tenant_access_tokens
  ADD CONSTRAINT platform_tenant_access_tokens_scopes_check CHECK (
    cardinality(scopes) > 0 AND
    scopes <@ ARRAY[
      'platform_tenant:usage:read',
      'platform_tenant:statements:read'
    ]::text[]
  );
-- +goose StatementEnd
