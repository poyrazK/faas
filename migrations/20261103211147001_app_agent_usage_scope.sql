-- filename: 20261103211147001_app_agent_usage_scope.sql
-- migration-compat: expand
-- +goose NO TRANSACTION
-- +goose Up
SET lock_timeout = '5s';
-- ADR-20261103211147: usage:read stays confined by the app-bound principal.
-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='deploy_tokens'::regclass AND conname='deploy_tokens_agent_usage_scopes_chk') THEN
  ALTER TABLE deploy_tokens ADD CONSTRAINT deploy_tokens_agent_usage_scopes_chk CHECK (
   cardinality(scopes)>0 AND
   ((NOT ('agent'=ANY(scopes)) AND scopes <@ ARRAY['deploy:write']::text[]) OR
    ('agent'=ANY(scopes) AND 'apps:read'=ANY(scopes) AND scopes <@ ARRAY['agent','apps:read','deploy:write','env:write','secrets:request','storage:manage','usage:read']::text[]))
  ) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE deploy_tokens VALIDATE CONSTRAINT deploy_tokens_agent_usage_scopes_chk;
ALTER TABLE deploy_tokens DROP CONSTRAINT IF EXISTS deploy_tokens_agent_scopes_chk;
RESET lock_timeout;

-- +goose Down
SET lock_timeout = '5s';
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM deploy_tokens WHERE 'usage:read'=ANY(scopes)) THEN
  RAISE EXCEPTION 'revoke and remove app-agent usage credentials before rollback';
 END IF;
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
ALTER TABLE deploy_tokens DROP CONSTRAINT IF EXISTS deploy_tokens_agent_usage_scopes_chk;
RESET lock_timeout;
