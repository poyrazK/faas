-- +goose Up
-- Preserve a monotonic revision when returning to live app inheritance.
ALTER TABLE project_environment_cache_policies ADD COLUMN inherited boolean NOT NULL DEFAULT false;
ALTER TABLE project_environment_cache_policies ADD CONSTRAINT environment_cache_inherited_empty CHECK (NOT inherited OR rules='[]'::jsonb);

-- +goose Down
-- Refuse to silently turn an inherited revision into a cache-disabled override.
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM project_environment_cache_policies WHERE inherited) THEN
  RAISE EXCEPTION 'restore or remove inherited cache policy revisions before rollback';
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE project_environment_cache_policies DROP CONSTRAINT environment_cache_inherited_empty;
ALTER TABLE project_environment_cache_policies DROP COLUMN inherited;
