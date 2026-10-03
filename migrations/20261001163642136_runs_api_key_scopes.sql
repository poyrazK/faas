-- +goose Up
-- +goose StatementBegin
-- Keep the database check aligned with api.validScopes while adding
-- least-privilege Runs credentials for agents. The prior OIDC migration
-- accidentally dropped the already-supported event producer scopes from
-- this check, so retain them here as well.
ALTER TABLE api_keys DROP CONSTRAINT IF EXISTS api_keys_scopes_vocab_chk;
ALTER TABLE api_keys ADD CONSTRAINT api_keys_scopes_vocab_chk CHECK (
    scopes <@ ARRAY[
        'admin', 'apps:read', 'deploy:write', 'secrets:read', 'secrets:write',
        'usage:read', 'env:read', 'env:write', 'registry_credentials:read',
        'registry_credentials:write', 'upstreams:write', 'metrics:write',
        'delayed_tasks:read', 'delayed_tasks:write', 'events:publish',
        'queues:send', 'storage:manage', 'storage:read', 'storage:write',
        'postgres:manage', 'postgres:read', 'github:manage',
        'project_environments:read', 'project_environments:qualify',
        'runs:read', 'runs:write'
    ]::text[] AND cardinality(scopes) > 0
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM api_keys
        WHERE scopes && ARRAY['runs:read', 'runs:write']::text[]
    ) THEN
        RAISE EXCEPTION 'revoke Runs-scoped API keys before rolling back this migration';
    END IF;
END $$;

ALTER TABLE api_keys DROP CONSTRAINT IF EXISTS api_keys_scopes_vocab_chk;
ALTER TABLE api_keys ADD CONSTRAINT api_keys_scopes_vocab_chk CHECK (
    scopes <@ ARRAY[
        'admin', 'apps:read', 'deploy:write', 'secrets:read', 'secrets:write',
        'usage:read', 'env:read', 'env:write', 'registry_credentials:read',
        'registry_credentials:write', 'upstreams:write', 'metrics:write',
        'delayed_tasks:read', 'delayed_tasks:write', 'events:publish',
        'queues:send', 'storage:manage', 'storage:read', 'storage:write',
        'postgres:manage', 'postgres:read', 'github:manage',
        'project_environments:read', 'project_environments:qualify'
    ]::text[] AND cardinality(scopes) > 0
);
-- +goose StatementEnd
