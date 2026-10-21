-- migration-compat: contract
-- Company namespaces for atomic release state (ADR-20261021081828).
-- The release tables added for ADR-20261019120000..55 referenced accounts(id),
-- but an adopted organization keeps its projects in its own resource
-- namespace, so their submissions, workers, canaries, secret snapshots,
-- service additions and migration contracts could not be recorded. Point each
-- foreign key at resource_namespaces(id), which holds every account and
-- organization, keeping its ON DELETE behavior. Retiring the old keys is a
-- contract step, as in 20261019190114312_org_project_ownership_online; the
-- previous release writes only account namespaces, which both keys accept.
-- Applied online (ADR-202610171900): every goose statement is its own short
-- transaction locking one existing table plus resource_namespaces. Each key is
-- replaced in three steps (add NOT VALID, validate, retire the old key), and
-- every step is idempotent.
-- +goose NO TRANSACTION
-- +goose Up
SET lock_timeout = '5s';
-- project_submissions: add the namespace foreign key NOT VALID
-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='project_submissions'::regclass AND conname='project_submissions_namespace_fkey') AND EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='project_submissions'::regclass AND conname='project_submissions_account_id_fkey' AND confrelid='accounts'::regclass) THEN
  ALTER TABLE project_submissions ADD CONSTRAINT project_submissions_namespace_fkey FOREIGN KEY (account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
-- deployment_project_release_workers: add the namespace foreign key NOT VALID
-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='deployment_project_release_workers'::regclass AND conname='deployment_project_release_workers_namespace_fkey') AND EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='deployment_project_release_workers'::regclass AND conname='deployment_project_release_workers_account_id_fkey' AND confrelid='accounts'::regclass) THEN
  ALTER TABLE deployment_project_release_workers ADD CONSTRAINT deployment_project_release_workers_namespace_fkey FOREIGN KEY (account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
-- project_release_canaries: add the namespace foreign key NOT VALID
-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='project_release_canaries'::regclass AND conname='project_release_canaries_namespace_fkey') AND EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='project_release_canaries'::regclass AND conname='project_release_canaries_account_id_fkey' AND confrelid='accounts'::regclass) THEN
  ALTER TABLE project_release_canaries ADD CONSTRAINT project_release_canaries_namespace_fkey FOREIGN KEY (account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
-- project_release_secret_bundles: add the namespace foreign key NOT VALID
-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='project_release_secret_bundles'::regclass AND conname='project_release_secret_bundles_namespace_fkey') AND EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='project_release_secret_bundles'::regclass AND conname='project_release_secret_bundles_account_id_fkey' AND confrelid='accounts'::regclass) THEN
  ALTER TABLE project_release_secret_bundles ADD CONSTRAINT project_release_secret_bundles_namespace_fkey FOREIGN KEY (account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
-- project_release_additions: add the namespace foreign key NOT VALID
-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='project_release_additions'::regclass AND conname='project_release_additions_namespace_fkey') AND EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='project_release_additions'::regclass AND conname='project_release_additions_account_id_fkey' AND confrelid='accounts'::regclass) THEN
  ALTER TABLE project_release_additions ADD CONSTRAINT project_release_additions_namespace_fkey FOREIGN KEY (account_id) REFERENCES resource_namespaces(id) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
-- project_migration_contracts: add the namespace foreign key NOT VALID
-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='project_migration_contracts'::regclass AND conname='project_migration_contracts_namespace_fkey') AND EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='project_migration_contracts'::regclass AND conname='project_migration_contracts_account_id_fkey' AND confrelid='accounts'::regclass) THEN
  ALTER TABLE project_migration_contracts ADD CONSTRAINT project_migration_contracts_namespace_fkey FOREIGN KEY (account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
-- project_submissions: validate the namespace foreign key
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='project_submissions'::regclass AND conname='project_submissions_namespace_fkey' AND NOT convalidated) THEN
  ALTER TABLE project_submissions VALIDATE CONSTRAINT project_submissions_namespace_fkey;
 END IF;
END $$;
-- +goose StatementEnd
-- deployment_project_release_workers: validate the namespace foreign key
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='deployment_project_release_workers'::regclass AND conname='deployment_project_release_workers_namespace_fkey' AND NOT convalidated) THEN
  ALTER TABLE deployment_project_release_workers VALIDATE CONSTRAINT deployment_project_release_workers_namespace_fkey;
 END IF;
END $$;
-- +goose StatementEnd
-- project_release_canaries: validate the namespace foreign key
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='project_release_canaries'::regclass AND conname='project_release_canaries_namespace_fkey' AND NOT convalidated) THEN
  ALTER TABLE project_release_canaries VALIDATE CONSTRAINT project_release_canaries_namespace_fkey;
 END IF;
END $$;
-- +goose StatementEnd
-- project_release_secret_bundles: validate the namespace foreign key
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='project_release_secret_bundles'::regclass AND conname='project_release_secret_bundles_namespace_fkey' AND NOT convalidated) THEN
  ALTER TABLE project_release_secret_bundles VALIDATE CONSTRAINT project_release_secret_bundles_namespace_fkey;
 END IF;
END $$;
-- +goose StatementEnd
-- project_release_additions: validate the namespace foreign key
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='project_release_additions'::regclass AND conname='project_release_additions_namespace_fkey' AND NOT convalidated) THEN
  ALTER TABLE project_release_additions VALIDATE CONSTRAINT project_release_additions_namespace_fkey;
 END IF;
END $$;
-- +goose StatementEnd
-- project_migration_contracts: validate the namespace foreign key
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='project_migration_contracts'::regclass AND conname='project_migration_contracts_namespace_fkey' AND NOT convalidated) THEN
  ALTER TABLE project_migration_contracts VALIDATE CONSTRAINT project_migration_contracts_namespace_fkey;
 END IF;
END $$;
-- +goose StatementEnd
-- project_submissions: retire the accounts foreign key
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='project_submissions'::regclass AND conname='project_submissions_namespace_fkey') THEN
  ALTER TABLE project_submissions DROP CONSTRAINT IF EXISTS project_submissions_account_id_fkey;
  ALTER TABLE project_submissions RENAME CONSTRAINT project_submissions_namespace_fkey TO project_submissions_account_id_fkey;
 END IF;
END $$;
-- +goose StatementEnd
-- deployment_project_release_workers: retire the accounts foreign key
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='deployment_project_release_workers'::regclass AND conname='deployment_project_release_workers_namespace_fkey') THEN
  ALTER TABLE deployment_project_release_workers DROP CONSTRAINT IF EXISTS deployment_project_release_workers_account_id_fkey;
  ALTER TABLE deployment_project_release_workers RENAME CONSTRAINT deployment_project_release_workers_namespace_fkey TO deployment_project_release_workers_account_id_fkey;
 END IF;
END $$;
-- +goose StatementEnd
-- project_release_canaries: retire the accounts foreign key
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='project_release_canaries'::regclass AND conname='project_release_canaries_namespace_fkey') THEN
  ALTER TABLE project_release_canaries DROP CONSTRAINT IF EXISTS project_release_canaries_account_id_fkey;
  ALTER TABLE project_release_canaries RENAME CONSTRAINT project_release_canaries_namespace_fkey TO project_release_canaries_account_id_fkey;
 END IF;
END $$;
-- +goose StatementEnd
-- project_release_secret_bundles: retire the accounts foreign key
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='project_release_secret_bundles'::regclass AND conname='project_release_secret_bundles_namespace_fkey') THEN
  ALTER TABLE project_release_secret_bundles DROP CONSTRAINT IF EXISTS project_release_secret_bundles_account_id_fkey;
  ALTER TABLE project_release_secret_bundles RENAME CONSTRAINT project_release_secret_bundles_namespace_fkey TO project_release_secret_bundles_account_id_fkey;
 END IF;
END $$;
-- +goose StatementEnd
-- project_release_additions: retire the accounts foreign key
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='project_release_additions'::regclass AND conname='project_release_additions_namespace_fkey') THEN
  ALTER TABLE project_release_additions DROP CONSTRAINT IF EXISTS project_release_additions_account_id_fkey;
  ALTER TABLE project_release_additions RENAME CONSTRAINT project_release_additions_namespace_fkey TO project_release_additions_account_id_fkey;
 END IF;
END $$;
-- +goose StatementEnd
-- project_migration_contracts: retire the accounts foreign key
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='project_migration_contracts'::regclass AND conname='project_migration_contracts_namespace_fkey') THEN
  ALTER TABLE project_migration_contracts DROP CONSTRAINT IF EXISTS project_migration_contracts_account_id_fkey;
  ALTER TABLE project_migration_contracts RENAME CONSTRAINT project_migration_contracts_namespace_fkey TO project_migration_contracts_account_id_fkey;
 END IF;
END $$;
-- +goose StatementEnd
RESET lock_timeout;
-- +goose Down
SET lock_timeout = '5s';
ALTER TABLE project_submissions DROP CONSTRAINT project_submissions_account_id_fkey;
ALTER TABLE project_submissions ADD CONSTRAINT project_submissions_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE NOT VALID;
ALTER TABLE deployment_project_release_workers DROP CONSTRAINT deployment_project_release_workers_account_id_fkey;
ALTER TABLE deployment_project_release_workers ADD CONSTRAINT deployment_project_release_workers_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE NOT VALID;
ALTER TABLE project_release_canaries DROP CONSTRAINT project_release_canaries_account_id_fkey;
ALTER TABLE project_release_canaries ADD CONSTRAINT project_release_canaries_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE NOT VALID;
ALTER TABLE project_release_secret_bundles DROP CONSTRAINT project_release_secret_bundles_account_id_fkey;
ALTER TABLE project_release_secret_bundles ADD CONSTRAINT project_release_secret_bundles_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE NOT VALID;
ALTER TABLE project_release_additions DROP CONSTRAINT project_release_additions_account_id_fkey;
ALTER TABLE project_release_additions ADD CONSTRAINT project_release_additions_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) NOT VALID;
ALTER TABLE project_migration_contracts DROP CONSTRAINT project_migration_contracts_account_id_fkey;
ALTER TABLE project_migration_contracts ADD CONSTRAINT project_migration_contracts_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE NOT VALID;
RESET lock_timeout;
