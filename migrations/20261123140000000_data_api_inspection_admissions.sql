-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
-- Monotonic revisions detect a change-and-revert between admission and
-- verification. Only fields that affect the selected target are included.
ALTER TABLE managed_postgres_bindings ADD COLUMN data_api_inspection_revision bigint NOT NULL DEFAULT 1;
ALTER TABLE managed_postgres_databases ADD COLUMN data_api_inspection_revision bigint NOT NULL DEFAULT 1;
ALTER TABLE managed_postgres_bindings ADD CONSTRAINT managed_postgres_bindings_data_api_inspection_revision_check CHECK (data_api_inspection_revision > 0) NOT VALID;
ALTER TABLE managed_postgres_databases ADD CONSTRAINT managed_postgres_databases_data_api_inspection_revision_check CHECK (data_api_inspection_revision > 0) NOT VALID;

-- +goose StatementBegin
CREATE FUNCTION bump_data_api_binding_inspection_revision() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF ROW(NEW.account_id, NEW.database_account_id, NEW.database_id, NEW.app_id, NEW.scope, NEW.environment_key, NEW.access, NEW.state, NEW.deleted_at, NEW.credential_generation, NEW.credential_ref, NEW.provider_identity_id, NEW.rotation_previous_generation, NEW.cutover_id)
     IS DISTINCT FROM ROW(OLD.account_id, OLD.database_account_id, OLD.database_id, OLD.app_id, OLD.scope, OLD.environment_key, OLD.access, OLD.state, OLD.deleted_at, OLD.credential_generation, OLD.credential_ref, OLD.provider_identity_id, OLD.rotation_previous_generation, OLD.cutover_id) THEN
    NEW.data_api_inspection_revision := OLD.data_api_inspection_revision + 1;
  ELSE
    NEW.data_api_inspection_revision := OLD.data_api_inspection_revision;
  END IF;
  RETURN NEW;
END;
$$;
-- +goose StatementEnd
-- Run after the existing BEFORE UPDATE triggers that may normalize ownership.
CREATE TRIGGER zz_data_api_binding_inspection_revision BEFORE UPDATE ON managed_postgres_bindings FOR EACH ROW EXECUTE FUNCTION bump_data_api_binding_inspection_revision();

-- +goose StatementBegin
CREATE FUNCTION bump_data_api_database_inspection_revision() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF ROW(NEW.account_id, NEW.org_id, NEW.cutover_id, NEW.backend_id, NEW.backend_fingerprint, NEW.provider_resource_id, NEW.data_resource_id, NEW.state, NEW.deleted_at, NEW.desired_generation, NEW.observed_generation)
     IS DISTINCT FROM ROW(OLD.account_id, OLD.org_id, OLD.cutover_id, OLD.backend_id, OLD.backend_fingerprint, OLD.provider_resource_id, OLD.data_resource_id, OLD.state, OLD.deleted_at, OLD.desired_generation, OLD.observed_generation) THEN
    NEW.data_api_inspection_revision := OLD.data_api_inspection_revision + 1;
  ELSE
    NEW.data_api_inspection_revision := OLD.data_api_inspection_revision;
  END IF;
  RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER zz_data_api_database_inspection_revision BEFORE UPDATE ON managed_postgres_databases FOR EACH ROW EXECUTE FUNCTION bump_data_api_database_inspection_revision();

CREATE TABLE data_api_inspection_admissions (
  task_id uuid PRIMARY KEY REFERENCES app_tasks(id) ON DELETE CASCADE,
  account_id uuid NOT NULL,
  app_id uuid NOT NULL,
  deployment_id uuid NOT NULL,
  binding_id uuid NOT NULL REFERENCES managed_postgres_bindings(id) ON DELETE CASCADE,
  database_id uuid NOT NULL REFERENCES managed_postgres_databases(id) ON DELETE CASCADE,
  table_name text NOT NULL CHECK (table_name ~ '^[A-Za-z_][A-Za-z0-9_]{0,62}$'),
  nonce text NOT NULL CHECK (nonce ~ '^[0-9a-f]{64}$'),
  binding_revision bigint NOT NULL CHECK (binding_revision > 0),
  database_revision bigint NOT NULL CHECK (database_revision > 0),
  runtime_generation bigint NOT NULL CHECK (runtime_generation > 0),
  credential_generation bigint NOT NULL CHECK (credential_generation > 0),
  target_digest text NOT NULL CHECK (target_digest ~ '^[0-9a-f]{64}$'),
  trust_digest text NOT NULL CHECK (trust_digest ~ '^[0-9a-f]{64}$')
);

-- +goose Down
DROP TABLE data_api_inspection_admissions;
DROP TRIGGER zz_data_api_binding_inspection_revision ON managed_postgres_bindings;
DROP FUNCTION bump_data_api_binding_inspection_revision();
DROP TRIGGER zz_data_api_database_inspection_revision ON managed_postgres_databases;
DROP FUNCTION bump_data_api_database_inspection_revision();
ALTER TABLE managed_postgres_bindings DROP COLUMN data_api_inspection_revision;
ALTER TABLE managed_postgres_databases DROP COLUMN data_api_inspection_revision;
