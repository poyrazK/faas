-- +goose Up
-- +goose StatementBegin
ALTER TABLE api_keys DROP CONSTRAINT IF EXISTS api_keys_scopes_vocab_chk;
ALTER TABLE api_keys ADD CONSTRAINT api_keys_scopes_vocab_chk CHECK (
    scopes <@ ARRAY[
        'admin', 'apps:read', 'deploy:write', 'secrets:read', 'secrets:write',
        'usage:read', 'env:read', 'env:write', 'registry_credentials:read',
        'registry_credentials:write', 'upstreams:write', 'storage:manage',
        'storage:read', 'storage:write', 'postgres:manage', 'postgres:read'
    ]::text[] AND cardinality(scopes) > 0
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE api_keys DROP CONSTRAINT IF EXISTS api_keys_scopes_vocab_chk;
ALTER TABLE api_keys ADD CONSTRAINT api_keys_scopes_vocab_chk CHECK (
    scopes <@ ARRAY[
        'admin', 'apps:read', 'deploy:write', 'secrets:read', 'secrets:write',
        'usage:read', 'env:read', 'env:write', 'registry_credentials:read',
        'registry_credentials:write', 'upstreams:write', 'storage:manage',
        'storage:read', 'storage:write'
    ]::text[] AND cardinality(scopes) > 0
);
-- +goose StatementEnd
