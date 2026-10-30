-- filename: 20261030070000000_github_preview_bot_draft_prs.sql
-- migration-compat: expand

-- ADR-20261030070000: a project chooses whether pull requests opened by bots
-- (Dependabot, Renovate) and draft pull requests get PR previews. Bot PRs are
-- skipped by default so they do not use up the preview allowance; drafts keep
-- their previews. Constant defaults make both columns metadata-only. An older
-- apid ignores them and an older githubd previews every PR, as before.

-- +goose Up
SET LOCAL lock_timeout = '5s';

ALTER TABLE github_deploy_policies ADD COLUMN IF NOT EXISTS preview_bot_prs boolean NOT NULL DEFAULT false;
ALTER TABLE github_deploy_policies ADD COLUMN IF NOT EXISTS preview_draft_prs boolean NOT NULL DEFAULT true;

-- +goose Down
SET LOCAL lock_timeout = '5s';

ALTER TABLE github_deploy_policies DROP COLUMN IF EXISTS preview_draft_prs;
ALTER TABLE github_deploy_policies DROP COLUMN IF EXISTS preview_bot_prs;
