-- filename: 20261103155008001_app_agent_tokens.sql
-- migration-compat: expand
-- ADR-20261103155008: reuse app-bound credentials for headless MCP agents.
-- +goose NO TRANSACTION
-- +goose Up
SET lock_timeout = '5s';
-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='deploy_tokens'::regclass AND conname='deploy_tokens_agent_scopes_chk') THEN
  ALTER TABLE deploy_tokens ADD CONSTRAINT deploy_tokens_agent_scopes_chk CHECK (
   cardinality(scopes)>0 AND
   ((NOT ('agent'=ANY(scopes)) AND scopes <@ ARRAY['deploy:write']::text[]) OR
    ('agent'=ANY(scopes) AND 'apps:read'=ANY(scopes) AND scopes <@ ARRAY['agent','apps:read','deploy:write','env:write','secrets:request','storage:manage']::text[]))
  ) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE deploy_tokens VALIDATE CONSTRAINT deploy_tokens_agent_scopes_chk;
ALTER TABLE deploy_tokens DROP CONSTRAINT IF EXISTS deploy_tokens_scopes_chk;

-- +goose Down
SET lock_timeout = '5s';
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM deploy_tokens WHERE 'agent'=ANY(scopes)) THEN
  RAISE EXCEPTION 'revoke and remove app-agent tokens before rollback';
 END IF;
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='deploy_tokens'::regclass AND conname='deploy_tokens_scopes_chk') THEN
  ALTER TABLE deploy_tokens ADD CONSTRAINT deploy_tokens_scopes_chk CHECK (scopes <@ ARRAY['deploy:write']::text[] AND cardinality(scopes)>0) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE deploy_tokens VALIDATE CONSTRAINT deploy_tokens_scopes_chk;
ALTER TABLE deploy_tokens DROP CONSTRAINT IF EXISTS deploy_tokens_agent_scopes_chk;
