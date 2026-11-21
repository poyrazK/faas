-- filename: 20261119170402100_api_key_data_read_scope.sql
-- migration-compat: expand
-- ADR-20261117215858: data:read reads customer data: database rows, message bodies, workflow payloads.
-- Keep the database check aligned with api.validScopes. The widened check
-- replaces api_keys_scopes_vocab_v4_chk online under a new name,
-- api_keys_scopes_vocab_v5_chk: added NOT VALID, validated, then the old one
-- dropped, each step committing on its own and guarded so a rerun converges.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';
-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'api_keys'::regclass AND conname = 'api_keys_scopes_vocab_v5_chk') THEN
  ALTER TABLE api_keys ADD CONSTRAINT api_keys_scopes_vocab_v5_chk CHECK (
    scopes <@ ARRAY[
        'admin', 'apps:read', 'deploy:write', 'secrets:read', 'secrets:write',
        'secrets:request', 'usage:read', 'env:read', 'env:write',
        'registry_credentials:read', 'registry_credentials:write',
        'upstreams:write', 'metrics:write', 'delayed_tasks:read',
        'delayed_tasks:write', 'events:publish', 'queues:send', 'queues:consume',
        'storage:manage', 'storage:read', 'storage:write', 'postgres:manage',
        'postgres:read', 'data:read', 'github:manage', 'project_environments:read',
        'project_environments:qualify', 'runs:read', 'runs:write', 'agent'
    ]::text[] AND cardinality(scopes) > 0
  ) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE api_keys VALIDATE CONSTRAINT api_keys_scopes_vocab_v5_chk;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE api_keys DROP CONSTRAINT IF EXISTS api_keys_scopes_vocab_v4_chk;
-- +goose StatementEnd

-- +goose Down
SET lock_timeout = '5s';
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM api_keys WHERE scopes && ARRAY['data:read']::text[]) THEN
  RAISE EXCEPTION 'revoke data:read API keys before rolling back this migration';
 END IF;
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'api_keys'::regclass AND conname = 'api_keys_scopes_vocab_v4_chk') THEN
  ALTER TABLE api_keys ADD CONSTRAINT api_keys_scopes_vocab_v4_chk CHECK (
    scopes <@ ARRAY[
        'admin', 'apps:read', 'deploy:write', 'secrets:read', 'secrets:write',
        'secrets:request', 'usage:read', 'env:read', 'env:write',
        'registry_credentials:read', 'registry_credentials:write',
        'upstreams:write', 'metrics:write', 'delayed_tasks:read',
        'delayed_tasks:write', 'events:publish', 'queues:send', 'queues:consume',
        'storage:manage', 'storage:read', 'storage:write', 'postgres:manage',
        'postgres:read', 'github:manage', 'project_environments:read',
        'project_environments:qualify', 'runs:read', 'runs:write', 'agent'
    ]::text[] AND cardinality(scopes) > 0
  ) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE api_keys VALIDATE CONSTRAINT api_keys_scopes_vocab_v4_chk;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE api_keys DROP CONSTRAINT IF EXISTS api_keys_scopes_vocab_v5_chk;
-- +goose StatementEnd
