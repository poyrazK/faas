-- filename: 20261110081607686_binding_round7_11_fixes.sql
-- migration-compat: expand
-- ADR-20261110120000: environment workload qualification covers every
-- binding a serving workload can hold. `session` bindings are serving
-- credentials with read_write privileges on the direct endpoint, so apid
-- checks them with the read_write ACL probe and records receipts for them
-- like read_write. `data_api` and `auth` bindings carry their own qualified
-- privilege contracts (ADR-650, ADR-1190) that the ACL probe cannot judge;
-- they no longer block a graph's qualification deadline, which used to make
-- such an app's environment unqualifiable.
-- The widened CHECK is added under a new name NOT VALID and validated
-- separately before the old one is dropped. Every step is re-runnable.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';

-- +goose StatementBegin
DO $receipts$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'environment_qualification_postgres_receipts'::regclass AND conname = 'environment_qualification_postgres_receipts_access_v2_check') THEN
    ALTER TABLE environment_qualification_postgres_receipts ADD CONSTRAINT environment_qualification_postgres_receipts_access_v2_check
      CHECK (access IN ('read_write','read_only','migration','session')) NOT VALID;
  END IF;
END
$receipts$;
-- +goose StatementEnd

ALTER TABLE environment_qualification_postgres_receipts VALIDATE CONSTRAINT environment_qualification_postgres_receipts_access_v2_check;
ALTER TABLE environment_qualification_postgres_receipts DROP CONSTRAINT IF EXISTS environment_qualification_postgres_receipts_access_check;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION environment_binding_postgres_deadline(q environment_workload_qualification_requests, release_scope text, max_age_seconds bigint, restored_instance uuid) RETURNS timestamp with time zone
    LANGUAGE plpgsql STABLE
    AS $$
DECLARE b managed_postgres_bindings%ROWTYPE; s app_secrets%ROWTYPE; receipt environment_qualification_postgres_receipts%ROWTYPE;
 captured jsonb; restored jsonb; until_at timestamptz:='infinity';
BEGIN
 SELECT inputs->'secret_versions' INTO captured FROM environment_qualification_snapshot_receipts WHERE instance_id=q.reserved_instance_id;
 SELECT runtime_inputs->'secret_versions' INTO restored FROM environment_qualification_restore_receipts
  WHERE request_id=q.id AND attempt=q.attempt AND instance_id=restored_instance;
 FOR b IN SELECT * FROM managed_postgres_bindings WHERE app_id=q.app_id AND scope=release_scope AND state<>'deleted' ORDER BY id LOOP
  -- data_api and auth logins are qualified by their own evidence.
  IF b.access IN ('data_api','auth') THEN CONTINUE; END IF;
  IF b.access NOT IN ('read_write','read_only','migration','session') OR b.state<>'ready' OR b.rotation_previous_generation IS NOT NULL OR b.cutover_id IS NOT NULL THEN RETURN NULL; END IF;
  SELECT * INTO s FROM app_secrets WHERE app_id=q.app_id AND scope=release_scope AND key=b.environment_key;
  IF s.managed_postgres_binding_id IS DISTINCT FROM b.id OR s.managed_credential_generation IS DISTINCT FROM b.credential_generation OR s.delivery_version IS NULL THEN RETURN NULL; END IF;
  SELECT * INTO receipt FROM environment_qualification_postgres_receipts WHERE request_id=q.id AND attempt=q.attempt AND binding_id=b.id
   AND credential_generation=b.credential_generation AND delivery_version=s.delivery_version AND environment_key=b.environment_key AND access=b.access
   ORDER BY recorded_at DESC LIMIT 1;
  IF receipt.request_id IS NULL OR receipt.recorded_at>clock_timestamp() THEN RETURN NULL; END IF;
  IF b.access<>'migration' AND ((captured->>(release_scope||'/'||b.environment_key)) IS DISTINCT FROM s.delivery_version::text OR
   (restored->>(release_scope||'/'||b.environment_key)) IS DISTINCT FROM s.delivery_version::text) THEN RETURN NULL; END IF;
  until_at:=least(until_at,receipt.recorded_at+max_age_seconds*interval '1 second');
 END LOOP;
 RETURN until_at;
END $$;
-- +goose StatementEnd

-- +goose Down
SET lock_timeout = '5s';

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION environment_binding_postgres_deadline(q environment_workload_qualification_requests, release_scope text, max_age_seconds bigint, restored_instance uuid) RETURNS timestamp with time zone
    LANGUAGE plpgsql STABLE
    AS $$
DECLARE b managed_postgres_bindings%ROWTYPE; s app_secrets%ROWTYPE; receipt environment_qualification_postgres_receipts%ROWTYPE;
 captured jsonb; restored jsonb; until_at timestamptz:='infinity';
BEGIN
 SELECT inputs->'secret_versions' INTO captured FROM environment_qualification_snapshot_receipts WHERE instance_id=q.reserved_instance_id;
 SELECT runtime_inputs->'secret_versions' INTO restored FROM environment_qualification_restore_receipts
  WHERE request_id=q.id AND attempt=q.attempt AND instance_id=restored_instance;
 FOR b IN SELECT * FROM managed_postgres_bindings WHERE app_id=q.app_id AND scope=release_scope AND state<>'deleted' ORDER BY id LOOP
  IF b.access NOT IN ('read_write','read_only','migration') OR b.state<>'ready' OR b.rotation_previous_generation IS NOT NULL OR b.cutover_id IS NOT NULL THEN RETURN NULL; END IF;
  SELECT * INTO s FROM app_secrets WHERE app_id=q.app_id AND scope=release_scope AND key=b.environment_key;
  IF s.managed_postgres_binding_id IS DISTINCT FROM b.id OR s.managed_credential_generation IS DISTINCT FROM b.credential_generation OR s.delivery_version IS NULL THEN RETURN NULL; END IF;
  SELECT * INTO receipt FROM environment_qualification_postgres_receipts WHERE request_id=q.id AND attempt=q.attempt AND binding_id=b.id
   AND credential_generation=b.credential_generation AND delivery_version=s.delivery_version AND environment_key=b.environment_key AND access=b.access
   ORDER BY recorded_at DESC LIMIT 1;
  IF receipt.request_id IS NULL OR receipt.recorded_at>clock_timestamp() THEN RETURN NULL; END IF;
  IF b.access<>'migration' AND ((captured->>(release_scope||'/'||b.environment_key)) IS DISTINCT FROM s.delivery_version::text OR
   (restored->>(release_scope||'/'||b.environment_key)) IS DISTINCT FROM s.delivery_version::text) THEN RETURN NULL; END IF;
  until_at:=least(until_at,receipt.recorded_at+max_age_seconds*interval '1 second');
 END LOOP;
 RETURN until_at;
END $$;
-- +goose StatementEnd

-- This constraint refuses downgrade while any session receipt remains.
-- +goose StatementBegin
DO $receipts$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'environment_qualification_postgres_receipts'::regclass AND conname = 'environment_qualification_postgres_receipts_access_check') THEN
    ALTER TABLE environment_qualification_postgres_receipts ADD CONSTRAINT environment_qualification_postgres_receipts_access_check
      CHECK (access IN ('read_write','read_only','migration'));
  END IF;
END
$receipts$;
-- +goose StatementEnd
ALTER TABLE environment_qualification_postgres_receipts DROP CONSTRAINT IF EXISTS environment_qualification_postgres_receipts_access_v2_check;
