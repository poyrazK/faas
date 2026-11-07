-- migration-compat: expand
-- Deployment authorization is per account; GitHub sign-in remains unique in
-- oauth_links. Existing rows and encrypted credentials retain their owners.
-- +goose NO TRANSACTION
-- +goose Up
SET statement_timeout = 0;
SET lock_timeout = '500ms';
-- Rebuild interrupted invalid indexes safely; the old global unique indexes
-- remain in force throughout this expansion.
DROP INDEX CONCURRENTLY IF EXISTS github_installations_installation_id_idx;
CREATE INDEX CONCURRENTLY github_installations_installation_id_idx ON github_installations (installation_id);
DROP INDEX CONCURRENTLY IF EXISTS projects_account_install_repo_uniq;
CREATE UNIQUE INDEX CONCURRENTLY projects_account_install_repo_uniq ON projects (account_id, install_id, repo_full_name)
  WHERE install_id IS NOT NULL AND repo_full_name IS NOT NULL;
DROP INDEX CONCURRENTLY IF EXISTS projects_install_repo_idx;
CREATE INDEX CONCURRENTLY projects_install_repo_idx ON projects (install_id, repo_full_name)
  WHERE install_id IS NOT NULL AND repo_full_name IS NOT NULL;
-- Old workers keep writing rows without account_id; derive it from the root.
ALTER TABLE pr_preview_sets ADD COLUMN IF NOT EXISTS account_id uuid REFERENCES accounts(id);
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION pr_preview_set_account() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 SELECT account_id INTO NEW.account_id FROM apps WHERE id=NEW.root_app_id;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS pr_preview_set_account_trg ON pr_preview_sets;
CREATE TRIGGER pr_preview_set_account_trg BEFORE INSERT OR UPDATE OF root_app_id ON pr_preview_sets
FOR EACH ROW EXECUTE FUNCTION pr_preview_set_account();
RESET lock_timeout;
RESET statement_timeout;

-- +goose Down
SET statement_timeout = 0;
SET lock_timeout = '500ms';
DROP TRIGGER IF EXISTS pr_preview_set_account_trg ON pr_preview_sets;
DROP FUNCTION IF EXISTS pr_preview_set_account();
DROP INDEX CONCURRENTLY IF EXISTS pr_preview_sets_account_identity;
ALTER TABLE pr_preview_sets DROP COLUMN account_id;
DROP INDEX CONCURRENTLY IF EXISTS projects_account_install_repo_uniq;
DROP INDEX CONCURRENTLY IF EXISTS projects_install_repo_idx;
DROP INDEX CONCURRENTLY IF EXISTS github_installations_installation_id_idx;
RESET lock_timeout;
RESET statement_timeout;
