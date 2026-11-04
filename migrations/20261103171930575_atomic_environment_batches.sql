-- filename: 20261103171930575_atomic_environment_batches.sql
-- migration-compat: expand
-- ADR-20261103171917: atomic variable batches and durable replay receipts.
-- +goose NO TRANSACTION
-- +goose Up
SET lock_timeout = '5s';
-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='app_config_revisions'::regclass AND conname='app_config_revisions_batch_source_chk') THEN
  ALTER TABLE app_config_revisions ADD CONSTRAINT app_config_revisions_batch_source_chk
   CHECK (source IN ('baseline','env.set','env.delete','env.batch','secret.set','secret.delete','secret.rotate','config.rollback','config.guard_restore')) NOT VALID;
 END IF;
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='app_config_revisions'::regclass AND conname='app_config_revisions_batch_key_chk') THEN
  ALTER TABLE app_config_revisions ADD CONSTRAINT app_config_revisions_batch_key_chk
   CHECK ((source IN ('baseline','env.batch','config.rollback','config.guard_restore')) = (config_key = '')) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE app_config_revisions VALIDATE CONSTRAINT app_config_revisions_batch_source_chk;
ALTER TABLE app_config_revisions VALIDATE CONSTRAINT app_config_revisions_batch_key_chk;
ALTER TABLE app_config_revisions DROP CONSTRAINT IF EXISTS app_config_revisions_source_check;
ALTER TABLE app_config_revisions DROP CONSTRAINT IF EXISTS app_config_revisions_check;

CREATE TABLE IF NOT EXISTS app_env_batch_receipts (
 app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 scope text NOT NULL CHECK (scope ~ '^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$'),
 request_id uuid NOT NULL,
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 request_sha256 bytea NOT NULL CHECK (octet_length(request_sha256)=32),
 receipt jsonb NOT NULL CHECK (jsonb_typeof(receipt)='object'),
 created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY (app_id,scope,request_id)
);
CREATE INDEX IF NOT EXISTS app_env_batch_receipts_created_idx ON app_env_batch_receipts(created_at);

-- Every writer, including N-1 daemons and guarded restores, participates in
-- the same scope lock. The app-wide lock also serializes cross-scope quotas.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION serialize_app_config_value_write() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE target record;
BEGIN
 FOR target IN
  SELECT DISTINCT app_id,scope FROM (
   SELECT CASE WHEN TG_OP='INSERT' THEN NULL ELSE OLD.app_id END app_id,
          CASE WHEN TG_OP='INSERT' THEN NULL ELSE OLD.scope END scope
   UNION ALL
   SELECT CASE WHEN TG_OP='DELETE' THEN NULL ELSE NEW.app_id END,
          CASE WHEN TG_OP='DELETE' THEN NULL ELSE NEW.scope END
  ) affected WHERE app_id IS NOT NULL ORDER BY app_id,scope
 LOOP
  PERFORM pg_advisory_xact_lock(hashtextextended('app_config:'||target.app_id::text||':'||target.scope,0));
  PERFORM pg_advisory_xact_lock(hashtextextended('app_config:'||target.app_id::text||':__env_quota__',0));
 END LOOP;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE OR REPLACE TRIGGER atomic_config_value_write BEFORE INSERT OR UPDATE OR DELETE ON app_envs
 FOR EACH ROW EXECUTE FUNCTION serialize_app_config_value_write();
CREATE OR REPLACE TRIGGER atomic_config_value_write BEFORE INSERT OR UPDATE OR DELETE ON app_secrets
 FOR EACH ROW EXECUTE FUNCTION serialize_app_config_value_write();
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='faas_runtime') THEN
  GRANT SELECT,INSERT,DELETE ON app_env_batch_receipts TO faas_runtime;
 END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
SET lock_timeout = '5s';
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM app_config_revisions WHERE source='env.batch') THEN
  RAISE EXCEPTION 'expire atomic batch history before rolling back this migration';
 END IF;
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='app_config_revisions'::regclass AND conname='app_config_revisions_source_check') THEN
  ALTER TABLE app_config_revisions ADD CONSTRAINT app_config_revisions_source_check
   CHECK (source IN ('baseline','env.set','env.delete','secret.set','secret.delete','secret.rotate','config.rollback','config.guard_restore')) NOT VALID;
 END IF;
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='app_config_revisions'::regclass AND conname='app_config_revisions_check') THEN
  ALTER TABLE app_config_revisions ADD CONSTRAINT app_config_revisions_check
   CHECK ((source IN ('baseline','config.rollback','config.guard_restore')) = (config_key = '')) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE app_config_revisions VALIDATE CONSTRAINT app_config_revisions_source_check;
ALTER TABLE app_config_revisions VALIDATE CONSTRAINT app_config_revisions_check;
ALTER TABLE app_config_revisions DROP CONSTRAINT IF EXISTS app_config_revisions_batch_source_chk;
ALTER TABLE app_config_revisions DROP CONSTRAINT IF EXISTS app_config_revisions_batch_key_chk;
DROP TRIGGER IF EXISTS atomic_config_value_write ON app_envs;
DROP TRIGGER IF EXISTS atomic_config_value_write ON app_secrets;
DROP FUNCTION IF EXISTS serialize_app_config_value_write();
DROP TABLE IF EXISTS app_env_batch_receipts;
