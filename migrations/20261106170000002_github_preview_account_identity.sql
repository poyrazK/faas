-- migration-compat: expand
-- +goose NO TRANSACTION
-- +goose Up
SET statement_timeout = 0;
SET lock_timeout = '500ms';
-- +goose StatementBegin
DO $$
BEGIN
 IF NOT EXISTS (
  SELECT 1 FROM pg_constraint
  WHERE conrelid='pr_preview_sets'::regclass AND conname='pr_preview_sets_account_not_null'
 ) THEN
  ALTER TABLE pr_preview_sets ADD CONSTRAINT pr_preview_sets_account_not_null CHECK(account_id IS NOT NULL) NOT VALID;
 END IF;
END;
$$;
-- +goose StatementEnd
ALTER TABLE pr_preview_sets VALIDATE CONSTRAINT pr_preview_sets_account_not_null;
DROP INDEX CONCURRENTLY IF EXISTS pr_preview_sets_account_identity;
CREATE UNIQUE INDEX CONCURRENTLY pr_preview_sets_account_identity ON pr_preview_sets(account_id,installation_id,repo_full_name,pr_number);
RESET lock_timeout;
RESET statement_timeout;
-- +goose Down
SET statement_timeout = 0;
SET lock_timeout = '500ms';
DROP INDEX CONCURRENTLY IF EXISTS pr_preview_sets_account_identity;
ALTER TABLE pr_preview_sets DROP CONSTRAINT pr_preview_sets_account_not_null;
RESET lock_timeout;
RESET statement_timeout;
