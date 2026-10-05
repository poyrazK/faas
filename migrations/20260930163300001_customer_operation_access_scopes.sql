-- +goose Up
-- +goose StatementBegin
ALTER TABLE platform_tenant_access_tokens DROP CONSTRAINT IF EXISTS platform_tenant_access_tokens_scopes_check;
ALTER TABLE platform_tenant_access_tokens ADD CONSTRAINT platform_tenant_access_tokens_scopes_check CHECK (
 cardinality(scopes)>0 AND scopes <@ ARRAY[
 'platform_tenant:usage:read','platform_tenant:statements:read',
 'platform_tenant:activation:read','platform_tenant:hostnames:manage',
 'platform_tenant:credentials:read','platform_tenant:credentials:manage',
 'platform_tenant:consumers:manage','platform_tenant:invocations:read',
 'platform_tenant:invocations:manage','platform_tenant:operations:read',
 'platform_tenant:operations:manage']::text[]
);
-- +goose StatementEnd

-- +goose Down
-- Forward-only: preserve scoped credentials when rolling back admission.
SELECT 1;
