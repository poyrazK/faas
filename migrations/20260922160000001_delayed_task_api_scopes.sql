-- +goose Up
-- +goose StatementBegin
-- Purpose-built delayed-task scopes let unattended producers schedule and
-- observe one-shot work without receiving general deployment authority.
-- metrics:write is included because it is already part of the Go closed set
-- and the custom-metrics route; this keeps the database vocabulary in parity.
ALTER TABLE api_keys DROP CONSTRAINT IF EXISTS api_keys_scopes_vocab_chk;
ALTER TABLE api_keys ADD CONSTRAINT api_keys_scopes_vocab_chk CHECK (
    scopes <@ ARRAY[
        'admin', 'apps:read', 'deploy:write', 'secrets:read', 'secrets:write',
        'usage:read', 'env:read', 'env:write', 'registry_credentials:read',
        'registry_credentials:write', 'upstreams:write', 'metrics:write',
        'delayed_tasks:read', 'delayed_tasks:write', 'storage:manage',
        'storage:read', 'storage:write', 'postgres:manage', 'postgres:read',
        'github:manage'
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
        'storage:read', 'storage:write', 'postgres:manage', 'postgres:read',
        'github:manage'
    ]::text[] AND cardinality(scopes) > 0
);
-- +goose StatementEnd
