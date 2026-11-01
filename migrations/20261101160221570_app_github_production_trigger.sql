-- filename: 20261101160221570_app_github_production_trigger.sql

-- ADR-20261101160300: an app bound to GitHub without a project chooses which
-- of the App webhook or GitHub Actions deploys its production branch. NULL
-- keeps the webhook, the default for every existing binding. Only apid writes
-- the column and githubd reads it; an older githubd ignores it.
-- migration-compat: expand
--
-- The CHECK is added NOT VALID and validated in its own statement, so apps
-- keep being written during the scan. Every step is re-runnable.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';

ALTER TABLE apps ADD COLUMN IF NOT EXISTS github_production_trigger text;

-- +goose StatementBegin
DO $trigger$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'apps'::regclass AND conname = 'apps_github_production_trigger_chk') THEN
    ALTER TABLE apps ADD CONSTRAINT apps_github_production_trigger_chk
      CHECK (github_production_trigger IS NULL OR github_production_trigger IN ('webhook', 'actions')) NOT VALID;
  END IF;
END
$trigger$;
-- +goose StatementEnd

ALTER TABLE apps VALIDATE CONSTRAINT apps_github_production_trigger_chk;

-- +goose Down
SET lock_timeout = '5s';

ALTER TABLE apps DROP CONSTRAINT IF EXISTS apps_github_production_trigger_chk;
ALTER TABLE apps DROP COLUMN IF EXISTS github_production_trigger;
