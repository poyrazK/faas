-- +goose Up
-- The projection is also the source for the migration's owned tables.
CREATE TABLE IF NOT EXISTS data_pipeline_connections (
 id uuid PRIMARY KEY,
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 name text NOT NULL CHECK (name ~ '^[a-z0-9][a-z0-9-]{1,38}[a-z0-9]$'),
 revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
 ciphertext bytea NOT NULL CHECK (octet_length(ciphertext) BETWEEN 1 AND 16384),
 identity text NOT NULL CHECK (identity ~ '^[0-9a-f]{64}$'),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(account_id,name), UNIQUE(account_id,id)
);
CREATE TABLE IF NOT EXISTS data_pipelines (
 id uuid PRIMARY KEY,
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 name text NOT NULL CHECK (name ~ '^[a-z0-9][a-z0-9-]{1,38}[a-z0-9]$'),
 source_connection_id uuid NOT NULL,
 database_id uuid NOT NULL REFERENCES managed_postgres_databases(id) ON DELETE CASCADE,
 tables jsonb NOT NULL CHECK (jsonb_typeof(tables)='array' AND jsonb_array_length(tables) BETWEEN 1 AND 16),
 read_subjects jsonb NOT NULL CHECK (jsonb_typeof(read_subjects)='array' AND jsonb_array_length(read_subjects) BETWEEN 1 AND 32),
 desired_state text NOT NULL DEFAULT 'running' CHECK (desired_state IN ('running','paused','deleted')),
 state text NOT NULL DEFAULT 'validating' CHECK (state IN ('validating','copying','catching_up','live','paused','blocked','deleting','deleted','resync_required')),
 revision bigint NOT NULL DEFAULT 1 CHECK (revision>0),
 observed_revision bigint NOT NULL DEFAULT 0 CHECK (observed_revision>=0 AND observed_revision<=revision),
 generation bigint NOT NULL DEFAULT 1 CHECK (generation>0),
 reason text NOT NULL DEFAULT '' CHECK (reason ~ '^[a-z0-9_]{0,80}$'),
 tables_ready integer NOT NULL DEFAULT 0 CHECK (tables_ready BETWEEN 0 AND 16),
 retained_wal_bytes bigint CHECK (retained_wal_bytes>=0),
 observed_at timestamptz,
 verification text NOT NULL DEFAULT 'unknown' CHECK (verification IN ('unknown','pending','passed','mismatch','inconclusive')),
 verified_at timestamptz,
 initialized boolean NOT NULL DEFAULT false,
 verify_requested boolean NOT NULL DEFAULT false,
 lease_token text NOT NULL DEFAULT '',
 lease_until timestamptz,
 retry_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(account_id,name),
 FOREIGN KEY(account_id,source_connection_id) REFERENCES data_pipeline_connections(account_id,id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX IF NOT EXISTS data_pipelines_owned_target ON data_pipelines(database_id) WHERE state<>'deleted';
CREATE INDEX IF NOT EXISTS data_pipelines_due ON data_pipelines(retry_at,id) WHERE state<>'deleted';


-- +goose StatementBegin
CREATE OR REPLACE FUNCTION data_pipeline_target_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_TABLE_NAME='data_pipelines' THEN
  PERFORM 1 FROM managed_postgres_databases WHERE id=NEW.database_id AND account_id=NEW.account_id
   AND state='ready' AND desired_generation=observed_generation FOR UPDATE;
  IF NOT FOUND OR EXISTS(SELECT 1 FROM managed_postgres_bindings WHERE database_id=NEW.database_id AND state<>'deleted') THEN
   RAISE EXCEPTION 'pipeline target must be an unbound ready database';
  END IF;
 ELSIF TG_TABLE_NAME='managed_postgres_bindings' THEN
  PERFORM 1 FROM managed_postgres_databases WHERE id=NEW.database_id FOR UPDATE;
  IF NEW.access<>'read_only' AND EXISTS(SELECT 1 FROM data_pipelines WHERE database_id=NEW.database_id AND state<>'deleted') THEN
   RAISE EXCEPTION 'pipeline mirrors permit only read-only application bindings';
  END IF;
 ELSIF TG_TABLE_NAME='managed_postgres_databases' THEN
  IF EXISTS(SELECT 1 FROM data_pipelines WHERE database_id=OLD.id AND state<>'deleted') THEN
   IF TG_OP='DELETE' THEN RAISE EXCEPTION 'delete the pipeline before deleting its mirror'; END IF;
   IF NEW.state<>'ready' OR NEW.desired_generation<>OLD.desired_generation THEN
    RAISE EXCEPTION 'active pipeline owns the mirror lifecycle';
   END IF;
  END IF;
 ELSIF TG_TABLE_NAME='accounts' THEN
  IF EXISTS(SELECT 1 FROM data_pipelines WHERE account_id=OLD.id AND state<>'deleted') THEN
   RAISE EXCEPTION 'delete data pipelines before deleting the account';
  END IF;
 END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF;
 RETURN NEW;
END; $$;
DROP TRIGGER IF EXISTS data_pipeline_reserve_target ON data_pipelines;
CREATE TRIGGER data_pipeline_reserve_target BEFORE INSERT ON data_pipelines FOR EACH ROW EXECUTE FUNCTION data_pipeline_target_guard();
DROP TRIGGER IF EXISTS data_pipeline_binding_guard ON managed_postgres_bindings;
CREATE TRIGGER data_pipeline_binding_guard BEFORE INSERT OR UPDATE OF access,database_id ON managed_postgres_bindings FOR EACH ROW EXECUTE FUNCTION data_pipeline_target_guard();
DROP TRIGGER IF EXISTS data_pipeline_database_guard ON managed_postgres_databases;
CREATE TRIGGER data_pipeline_database_guard BEFORE UPDATE OR DELETE ON managed_postgres_databases FOR EACH ROW EXECUTE FUNCTION data_pipeline_target_guard();
DROP TRIGGER IF EXISTS data_pipeline_account_guard ON accounts;
CREATE TRIGGER data_pipeline_account_guard BEFORE DELETE ON accounts FOR EACH ROW EXECUTE FUNCTION data_pipeline_target_guard();
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER data_pipeline_account_guard ON accounts;
DROP TRIGGER data_pipeline_database_guard ON managed_postgres_databases;
DROP TRIGGER data_pipeline_binding_guard ON managed_postgres_bindings;
DROP TRIGGER data_pipeline_reserve_target ON data_pipelines;
DROP FUNCTION data_pipeline_target_guard();
DROP TABLE data_pipelines;
DROP TABLE data_pipeline_connections;
