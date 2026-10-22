-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
CREATE TABLE IF NOT EXISTS data_pipeline_setups (
 id uuid PRIMARY KEY,
 account_id uuid NOT NULL,
 source_connection_id uuid NOT NULL,
 name text NOT NULL,
 request jsonb NOT NULL CHECK (jsonb_typeof(request)='object' AND octet_length(request::text)<=32768),
 mirror_plan jsonb NOT NULL CHECK (jsonb_typeof(mirror_plan)='object'),
 database_id uuid NOT NULL,
 mirror_name text NOT NULL,
 pipeline_id text NOT NULL DEFAULT '' CHECK (pipeline_id='' OR pipeline_id ~ '^[0-9a-f-]{36}$'),
 state text NOT NULL DEFAULT 'working' CHECK (state IN ('working','blocked','ready','cancelled')),
 phase text NOT NULL DEFAULT 'checking_source' CHECK (phase IN ('checking_source','provisioning','checking_mirror','starting','syncing')),
 reason text NOT NULL DEFAULT '' CHECK (reason ~ '^[a-z0-9_]{0,80}$'),
 revision bigint NOT NULL DEFAULT 1 CHECK (revision>0),
 lease_token text NOT NULL DEFAULT '',
 lease_until timestamptz,
 retry_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(account_id,name),
 FOREIGN KEY(account_id,source_connection_id) REFERENCES data_pipeline_connections(account_id,id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX IF NOT EXISTS data_pipeline_setups_source_active ON data_pipeline_setups(source_connection_id) WHERE state IN ('working','blocked');
CREATE INDEX IF NOT EXISTS data_pipeline_setups_due ON data_pipeline_setups(retry_at,id) WHERE state='working';

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION data_pipeline_setup_owner_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_TABLE_NAME='accounts' THEN
  IF EXISTS(SELECT 1 FROM data_pipeline_setups WHERE account_id=OLD.id AND state IN ('working','blocked')) THEN
   RAISE EXCEPTION 'stop data pipeline setups before deleting the account';
  END IF;
 ELSIF TG_TABLE_NAME='data_pipeline_connections' THEN
  IF EXISTS(SELECT 1 FROM data_pipeline_setups WHERE source_connection_id=OLD.id AND state IN ('working','blocked')) THEN
   RAISE EXCEPTION 'stop data pipeline setups before deleting the source';
  END IF;
 ELSIF EXISTS(SELECT 1 FROM data_pipeline_setups WHERE database_id=OLD.id AND state IN ('working','blocked')) THEN
  IF TG_OP='DELETE' THEN
   RAISE EXCEPTION 'stop data pipeline setups before deleting the mirror';
  END IF;
  IF NEW.desired_generation<>OLD.desired_generation OR NEW.state IN ('deleting','deleted') THEN
   RAISE EXCEPTION 'active data pipeline setup owns the mirror lifecycle';
  END IF;
 END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF;
 RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS data_pipeline_setup_account_guard ON accounts;
CREATE TRIGGER data_pipeline_setup_account_guard BEFORE DELETE ON accounts FOR EACH ROW EXECUTE FUNCTION data_pipeline_setup_owner_guard();
DROP TRIGGER IF EXISTS data_pipeline_setup_source_guard ON data_pipeline_connections;
CREATE TRIGGER data_pipeline_setup_source_guard BEFORE DELETE ON data_pipeline_connections FOR EACH ROW EXECUTE FUNCTION data_pipeline_setup_owner_guard();
DROP TRIGGER IF EXISTS data_pipeline_setup_database_guard ON managed_postgres_databases;
CREATE TRIGGER data_pipeline_setup_database_guard BEFORE UPDATE OR DELETE ON managed_postgres_databases FOR EACH ROW EXECUTE FUNCTION data_pipeline_setup_owner_guard();
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TRIGGER data_pipeline_setup_account_guard ON accounts;
DROP TRIGGER data_pipeline_setup_source_guard ON data_pipeline_connections;
DROP TRIGGER data_pipeline_setup_database_guard ON managed_postgres_databases;
DROP FUNCTION data_pipeline_setup_owner_guard();
DROP TABLE data_pipeline_setups;
