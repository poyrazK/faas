-- filename: 20261110164822932_github_installations_namespace_owner.sql
-- migration-compat: contract
-- An adopted organization owns a GitHub installation through its resource
-- namespace (ADR-20261110200000). github_installations.account_id referenced
-- accounts, which an organization's namespace is not; it now references
-- resource_namespaces, as projects.account_id does. Every account is also a
-- resource namespace (seeded and kept by the org resource lifecycle trigger),
-- so existing rows validate. The old foreign key is dropped last; the previous
-- release never relies on it, but dropping it is a contract change. Each step
-- is safe to replay.
-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
SET lock_timeout = '5s';
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint
                  WHERE conrelid = 'github_installations'::regclass
                    AND conname = 'github_installations_namespace_fkey') THEN
    ALTER TABLE github_installations
      ADD CONSTRAINT github_installations_namespace_fkey FOREIGN KEY (account_id)
      REFERENCES resource_namespaces(id) ON DELETE CASCADE NOT VALID;
  END IF;
END
$$;
-- +goose StatementEnd
-- +goose StatementBegin
SET lock_timeout = '5s';
ALTER TABLE github_installations VALIDATE CONSTRAINT github_installations_namespace_fkey;
-- +goose StatementEnd
-- +goose StatementBegin
SET lock_timeout = '5s';
ALTER TABLE github_installations DROP CONSTRAINT IF EXISTS github_installations_account_id_fkey;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET lock_timeout = '5s';
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint
                  WHERE conrelid = 'github_installations'::regclass
                    AND conname = 'github_installations_account_id_fkey') THEN
    ALTER TABLE github_installations
      ADD CONSTRAINT github_installations_account_id_fkey FOREIGN KEY (account_id)
      REFERENCES accounts(id) ON DELETE CASCADE NOT VALID;
  END IF;
END
$$;
-- +goose StatementEnd
-- +goose StatementBegin
SET lock_timeout = '5s';
ALTER TABLE github_installations DROP CONSTRAINT IF EXISTS github_installations_namespace_fkey;
-- +goose StatementEnd
