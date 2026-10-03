-- ADR-464: stage credentials without publishing or switching application bindings.
-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS managed_postgres_cutovers (
 id uuid PRIMARY KEY,
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 scope text NOT NULL CHECK (scope ~ '^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$'),
 source_database_id uuid NOT NULL REFERENCES managed_postgres_databases(id) ON DELETE CASCADE,
 target_database_id uuid NOT NULL REFERENCES managed_postgres_databases(id) ON DELETE CASCADE,
 source_backend_id text NOT NULL, source_backend_fingerprint text NOT NULL CHECK (length(source_backend_fingerprint)=64),
 source_resource_id text NOT NULL CHECK (length(source_resource_id)>0), source_generation bigint NOT NULL CHECK (source_generation>0),
 target_backend_id text NOT NULL, target_backend_fingerprint text NOT NULL CHECK (length(target_backend_fingerprint)=64),
 target_resource_id text NOT NULL CHECK (length(target_resource_id)>0), target_generation bigint NOT NULL CHECK (target_generation>0),
 state text NOT NULL CHECK (state IN ('preparing','prepared','cancelling','cancelled')),
 last_error_code text CHECK (last_error_code ~ '^[a-z][a-z0-9_]{0,62}$'),
 lease_token text, lease_until timestamptz,
 attempt_count integer NOT NULL DEFAULT 0 CHECK (attempt_count BETWEEN 0 AND 30),
 retry_at timestamptz NOT NULL, created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL,
 CHECK (source_database_id<>target_database_id),
 CHECK ((lease_token IS NULL)=(lease_until IS NULL))
);
CREATE UNIQUE INDEX IF NOT EXISTS managed_postgres_cutovers_active_app_scope_idx ON managed_postgres_cutovers(app_id,scope) WHERE state<>'cancelled';
CREATE INDEX IF NOT EXISTS managed_postgres_cutovers_due_idx ON managed_postgres_cutovers(retry_at,id) WHERE state IN ('preparing','cancelling');
CREATE TABLE IF NOT EXISTS managed_postgres_cutover_credentials (
 id uuid PRIMARY KEY,
 cutover_id uuid NOT NULL REFERENCES managed_postgres_cutovers(id) ON DELETE CASCADE,
 source_binding_id uuid NOT NULL REFERENCES managed_postgres_bindings(id) ON DELETE CASCADE,
 source_credential_generation bigint NOT NULL CHECK (source_credential_generation>0),
 environment_key text NOT NULL CHECK (environment_key ~ '^[A-Z_][A-Z0-9_]{0,126}$'),
 access text NOT NULL CHECK (access IN ('read_write','read_only','migration')),
 state text NOT NULL DEFAULT 'pending' CHECK (state IN ('pending','sealed','revoked')),
 provider_identity_id text, credential_ref text, ciphertext bytea, kid text, value_hash text,
 UNIQUE(cutover_id,source_binding_id), UNIQUE(cutover_id,environment_key),
 CHECK ((state='sealed' AND num_nonnulls(provider_identity_id,credential_ref,ciphertext,kid,value_hash)=5 AND length(provider_identity_id)>0 AND length(credential_ref)>0 AND length(ciphertext)>0 AND length(kid)>0 AND length(value_hash)>0)
 OR (state IN ('pending','revoked') AND provider_identity_id IS NULL AND credential_ref IS NULL AND ciphertext IS NULL AND kid IS NULL AND value_hash IS NULL))
);
ALTER TABLE managed_postgres_databases ADD COLUMN IF NOT EXISTS cutover_id uuid REFERENCES managed_postgres_cutovers(id) ON DELETE RESTRICT;
ALTER TABLE managed_postgres_bindings ADD COLUMN IF NOT EXISTS cutover_id uuid REFERENCES managed_postgres_cutovers(id) ON DELETE RESTRICT;

CREATE OR REPLACE FUNCTION guard_managed_postgres_cutover_database() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF OLD.cutover_id IS NOT NULL THEN
  IF TG_OP='DELETE' OR ROW(NEW.account_id,NEW.state,NEW.backend_id,NEW.backend_fingerprint,NEW.provider_resource_id,NEW.desired_generation,NEW.region,NEW.postgres_major,NEW.service_class,NEW.availability,NEW.scale_to_zero,NEW.storage_limit_bytes,NEW.restore_window_seconds)
     IS DISTINCT FROM ROW(OLD.account_id,OLD.state,OLD.backend_id,OLD.backend_fingerprint,OLD.provider_resource_id,OLD.desired_generation,OLD.region,OLD.postgres_major,OLD.service_class,OLD.availability,OLD.scale_to_zero,OLD.storage_limit_bytes,OLD.restore_window_seconds)
     OR (NEW.cutover_id IS DISTINCT FROM OLD.cutover_id AND (NEW.cutover_id IS NOT NULL OR NOT EXISTS (SELECT 1 FROM managed_postgres_cutovers WHERE id=OLD.cutover_id AND state='cancelled'))) THEN
   RAISE EXCEPTION 'database is pinned by a cutover' USING ERRCODE='23514', CONSTRAINT='managed_postgres_cutover_conflict';
  END IF;
 END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF;
 RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS managed_postgres_cutover_database_guard ON managed_postgres_databases;
CREATE TRIGGER managed_postgres_cutover_database_guard BEFORE UPDATE OR DELETE ON managed_postgres_databases FOR EACH ROW EXECUTE FUNCTION guard_managed_postgres_cutover_database();
CREATE OR REPLACE FUNCTION guard_managed_postgres_cutover_binding() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE pinned uuid;
BEGIN
 IF TG_OP IN ('UPDATE','DELETE') AND OLD.cutover_id IS NOT NULL THEN
  IF TG_OP='DELETE' OR (to_jsonb(NEW)-'cutover_id') IS DISTINCT FROM (to_jsonb(OLD)-'cutover_id')
   OR (NEW.cutover_id IS DISTINCT FROM OLD.cutover_id AND (NEW.cutover_id IS NOT NULL OR NOT EXISTS (SELECT 1 FROM managed_postgres_cutovers WHERE id=OLD.cutover_id AND state='cancelled'))) THEN
   RAISE EXCEPTION 'binding is pinned by a cutover' USING ERRCODE='23514', CONSTRAINT='managed_postgres_cutover_conflict';
  END IF;
 END IF;
 IF TG_OP='INSERT' OR (TG_OP='UPDATE' AND ROW(NEW.database_id,NEW.app_id,NEW.scope,NEW.environment_key) IS DISTINCT FROM ROW(OLD.database_id,OLD.app_id,OLD.scope,OLD.environment_key)) THEN
  SELECT cutover_id INTO pinned FROM managed_postgres_databases WHERE id=NEW.database_id FOR KEY SHARE;
  IF pinned IS NOT NULL THEN
   RAISE EXCEPTION 'database is pinned by a cutover' USING ERRCODE='23514', CONSTRAINT='managed_postgres_cutover_conflict';
  END IF;
 END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF;
 RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS managed_postgres_cutover_binding_guard ON managed_postgres_bindings;
CREATE TRIGGER managed_postgres_cutover_binding_guard BEFORE INSERT OR UPDATE OR DELETE ON managed_postgres_bindings FOR EACH ROW EXECUTE FUNCTION guard_managed_postgres_cutover_binding();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM managed_postgres_cutovers WHERE state<>'cancelled') THEN
  RAISE EXCEPTION 'cancel cutovers and revoke staged credentials before rollback' USING ERRCODE='23514', CONSTRAINT='managed_postgres_cutover_conflict';
 END IF;
END $$;
DROP TRIGGER managed_postgres_cutover_binding_guard ON managed_postgres_bindings;
DROP TRIGGER managed_postgres_cutover_database_guard ON managed_postgres_databases;
DROP FUNCTION guard_managed_postgres_cutover_binding();
DROP FUNCTION guard_managed_postgres_cutover_database();
ALTER TABLE managed_postgres_bindings DROP COLUMN cutover_id;
ALTER TABLE managed_postgres_databases DROP COLUMN cutover_id;
DROP TABLE managed_postgres_cutover_credentials;
DROP TABLE managed_postgres_cutovers;
-- +goose StatementEnd
