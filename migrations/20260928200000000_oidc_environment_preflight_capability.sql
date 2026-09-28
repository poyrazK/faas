-- +goose Up
-- +goose StatementBegin
-- Project-environment CI gets a separate least-privilege OIDC profile. Existing
-- exchanged bearers remain deploy:write; the new profile can only read
-- non-secret project environment state and write qualification receipts.
ALTER TABLE oidc_exchanged_tokens
    ADD COLUMN scopes text[] NOT NULL DEFAULT ARRAY['deploy:write']::text[]
    CHECK (
        scopes <@ ARRAY[
            'deploy:write', 'project_environments:read',
            'project_environments:qualify'
        ]::text[]
        AND cardinality(scopes) > 0
    );

ALTER TABLE api_keys DROP CONSTRAINT IF EXISTS api_keys_scopes_vocab_chk;
ALTER TABLE api_keys ADD CONSTRAINT api_keys_scopes_vocab_chk CHECK (
    scopes <@ ARRAY[
        'admin', 'apps:read', 'deploy:write', 'secrets:read', 'secrets:write',
        'usage:read', 'env:read', 'env:write', 'registry_credentials:read',
        'registry_credentials:write', 'upstreams:write', 'metrics:write',
        'delayed_tasks:read', 'delayed_tasks:write', 'storage:manage',
        'storage:read', 'storage:write', 'postgres:manage', 'postgres:read',
        'github:manage', 'project_environments:read', 'project_environments:qualify'
    ]::text[] AND cardinality(scopes) > 0
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Do not let an environment-only OIDC bearer survive removal of the scopes
-- column: older authentication code treats rows without scopes as deploy:write.
DELETE FROM oidc_exchanged_tokens
WHERE expires_at > now()
  AND scopes && ARRAY['project_environments:read', 'project_environments:qualify']::text[];

-- Persistent API keys need an explicit operator migration before this scope
-- vocabulary can be removed. Refuse rollback rather than silently changing
-- their access or leaving the database constraint out of sync.
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM api_keys
        WHERE scopes && ARRAY['project_environments:read', 'project_environments:qualify']::text[]
    ) THEN
        RAISE EXCEPTION 'revoke project-environment-scoped API keys before rolling back this migration';
    END IF;
END $$;

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
ALTER TABLE oidc_exchanged_tokens DROP COLUMN IF EXISTS scopes;
-- +goose StatementEnd
