-- filename: 20261101224106073_app_github_preview_enabled.sql

-- ADR-20261101160300: an app bound to GitHub without a project can turn its
-- pull-request previews off. NULL keeps them on, the default for every
-- existing binding. Only apid writes the column and githubd reads it; an older
-- githubd ignores it.
-- migration-compat: expand

-- +goose Up
SET lock_timeout = '5s';

ALTER TABLE apps ADD COLUMN IF NOT EXISTS github_preview_enabled boolean;

-- +goose Down
SET lock_timeout = '5s';

ALTER TABLE apps DROP COLUMN IF EXISTS github_preview_enabled;
