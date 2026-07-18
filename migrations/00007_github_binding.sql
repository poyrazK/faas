-- +goose Up
-- +goose StatementBegin

-- M7.5 patch: GitHub App install binding (review findings #1 + #2).
-- Until now the only place a (repo → installation_id) mapping lived
-- was in githubd's in-memory sync.Map — that path left
-- `installation_id = 1` hardcoded in pkg/githubd/checks.go, so every
-- account's CheckRun writes went out under the same bearer token.
-- This migration is the §11 least-privilege fix: persist the binding
-- alongside the app so a process restart, an audit, or a tenant
-- migration can recover it, and so the CheckRun path can look up
-- the real installation_id per app instead of guessing.
--
-- Schema notes:
--   * Each app can have at most one binding today (GitHub Apps
--     install once per app). We model it as 1:1 columns on apps
--     rather than a join table to keep the §11 audit row obvious
--     in `select * from apps` for ops.
--   * Columns are nullable so the migration is non-blocking — apps
--     that aren't GitHub-connected keep working unchanged.
--   * repo_full_name is "owner/name"; the dashboard's repo-picker
--     writes it after the user picks a repo from
--     ListInstallableRepos. production_branch is the branch the
--     webhook receiver filters on (default "main").
--   * The install_id + repo_full_name pair has a unique partial
--     index so two apps can't both claim the same installation
--     repo (that would split the CheckRun ownership ambiguously).

alter table apps
  add column if not exists github_install_id      bigint,
  add column if not exists github_repo_full_name  text,
  add column if not exists github_production_branch text;

create unique index if not exists apps_github_install_repo_uniq
  on apps (github_install_id, github_repo_full_name)
  where github_install_id is not null
    and github_repo_full_name is not null;

create index if not exists apps_github_install_id_idx
  on apps (github_install_id)
  where github_install_id is not null;

-- +goose StatementEnd
