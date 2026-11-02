-- filename: 20261102203228267_oidc_preview_read_scope.sql
-- migration-compat: expand
-- ADR-20261102203228: the OIDC preview-read capability issues bearers that
-- read the PR preview status of one app and nothing else, so pull request CI
-- can wait for its preview without a stored key. The widened check replaces
-- oidc_exchanged_tokens_scopes_check online under a new name,
-- oidc_exchanged_tokens_scopes_v2_check: added NOT VALID, validated, then the
-- old one dropped, each step committing on its own and guarded so a rerun
-- converges. The previous release never issues the scope.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';
-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'oidc_exchanged_tokens'::regclass AND conname = 'oidc_exchanged_tokens_scopes_v2_check') THEN
  ALTER TABLE oidc_exchanged_tokens ADD CONSTRAINT oidc_exchanged_tokens_scopes_v2_check CHECK (
    scopes <@ ARRAY[
        'deploy:write', 'project_environments:read',
        'project_environments:qualify', 'previews:read'
    ]::text[] AND cardinality(scopes) > 0
  ) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE oidc_exchanged_tokens VALIDATE CONSTRAINT oidc_exchanged_tokens_scopes_v2_check;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE oidc_exchanged_tokens DROP CONSTRAINT IF EXISTS oidc_exchanged_tokens_scopes_check;
-- +goose StatementEnd

-- +goose Down
SET lock_timeout = '5s';
-- +goose StatementBegin
-- An older release would read a preview-read bearer by its scopes only;
-- remove live ones rather than leave them unchecked.
DELETE FROM oidc_exchanged_tokens WHERE scopes && ARRAY['previews:read']::text[];
-- +goose StatementEnd
-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'oidc_exchanged_tokens'::regclass AND conname = 'oidc_exchanged_tokens_scopes_check') THEN
  ALTER TABLE oidc_exchanged_tokens ADD CONSTRAINT oidc_exchanged_tokens_scopes_check CHECK (
    scopes <@ ARRAY[
        'deploy:write', 'project_environments:read',
        'project_environments:qualify'
    ]::text[] AND cardinality(scopes) > 0
  ) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE oidc_exchanged_tokens VALIDATE CONSTRAINT oidc_exchanged_tokens_scopes_check;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE oidc_exchanged_tokens DROP CONSTRAINT IF EXISTS oidc_exchanged_tokens_scopes_v2_check;
-- +goose StatementEnd
