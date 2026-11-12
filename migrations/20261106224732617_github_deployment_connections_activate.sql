-- migration-compat: contract
-- Apply in a full release only, after every githubd worker supports account
-- fan-out. A unit/expand-only release retains the old exclusivity rules.
-- migration-lint: allow not-null pr_preview_sets.account_id: pr_preview_set_account derives account_id from the root app for legacy INSERTs; the expand release validates a non-null check before this catalog-only promotion.
-- +goose NO TRANSACTION
-- +goose Up
SET statement_timeout = 0;
SET lock_timeout = '500ms';
DROP INDEX CONCURRENTLY IF EXISTS github_installations_installation_id_uidx;
DROP INDEX CONCURRENTLY IF EXISTS projects_install_repo_uniq;
ALTER TABLE pr_preview_sets DROP CONSTRAINT IF EXISTS pr_preview_sets_pkey;
ALTER TABLE pr_preview_sets ALTER COLUMN account_id SET NOT NULL;
ALTER TABLE pr_preview_sets ADD CONSTRAINT pr_preview_sets_pkey PRIMARY KEY USING INDEX pr_preview_sets_account_identity;
RESET lock_timeout;
RESET statement_timeout;

-- +goose Down
SET statement_timeout = 0;
SET lock_timeout = '500ms';
DROP INDEX CONCURRENTLY IF EXISTS pr_preview_sets_legacy_identity;
CREATE UNIQUE INDEX CONCURRENTLY pr_preview_sets_legacy_identity ON pr_preview_sets(installation_id,repo_full_name,pr_number);
CREATE UNIQUE INDEX CONCURRENTLY pr_preview_sets_account_identity ON pr_preview_sets(account_id,installation_id,repo_full_name,pr_number);
ALTER TABLE pr_preview_sets DROP CONSTRAINT pr_preview_sets_pkey;
ALTER TABLE pr_preview_sets ADD CONSTRAINT pr_preview_sets_pkey PRIMARY KEY USING INDEX pr_preview_sets_legacy_identity;
-- Shared rows deliberately make this fail. Never delete authorizations or
-- projects to force an older exclusivity rule to succeed. Drop any invalid
-- index from an interrupted attempt before retrying the rollback.
CREATE UNIQUE INDEX CONCURRENTLY github_installations_installation_id_uidx ON github_installations (installation_id);
CREATE UNIQUE INDEX CONCURRENTLY projects_install_repo_uniq ON projects (install_id, repo_full_name)
  WHERE install_id IS NOT NULL AND repo_full_name IS NOT NULL;
RESET lock_timeout;
RESET statement_timeout;
