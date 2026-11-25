-- filename: 20261125145127001_native_postgres_replica_only_bindings.sql
-- migration-compat: expand
-- Standby-only bindings preserve their routing mode through rotation, cutover
-- and candidate qualification.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';

-- +goose StatementBegin
DO $replica$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'managed_postgres_bindings'::regclass AND conname = 'managed_postgres_bindings_access_v3_check') THEN
    ALTER TABLE managed_postgres_bindings ADD CONSTRAINT managed_postgres_bindings_access_v3_check
      CHECK (access IN ('read_write','read_only','migration','data_api','auth','session','replica_only')) NOT VALID;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'managed_postgres_cutover_credentials'::regclass AND conname = 'managed_postgres_cutover_credentials_access_v3_check') THEN
    ALTER TABLE managed_postgres_cutover_credentials ADD CONSTRAINT managed_postgres_cutover_credentials_access_v3_check
      CHECK (access IN ('read_write','read_only','migration','data_api','auth','session','replica_only')) NOT VALID;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'environment_qualification_postgres_receipts'::regclass AND conname = 'environment_qualification_postgres_receipts_access_v3_check') THEN
    ALTER TABLE environment_qualification_postgres_receipts ADD CONSTRAINT environment_qualification_postgres_receipts_access_v3_check
      CHECK (access IN ('read_write','read_only','migration','session','replica_only')) NOT VALID;
  END IF;
END
$replica$;
-- +goose StatementEnd

ALTER TABLE managed_postgres_bindings VALIDATE CONSTRAINT managed_postgres_bindings_access_v3_check;
ALTER TABLE managed_postgres_cutover_credentials VALIDATE CONSTRAINT managed_postgres_cutover_credentials_access_v3_check;
ALTER TABLE environment_qualification_postgres_receipts VALIDATE CONSTRAINT environment_qualification_postgres_receipts_access_v3_check;

ALTER TABLE managed_postgres_bindings DROP CONSTRAINT IF EXISTS managed_postgres_bindings_access_v2_check;
ALTER TABLE managed_postgres_cutover_credentials DROP CONSTRAINT IF EXISTS managed_postgres_cutover_credentials_access_v2_check;
ALTER TABLE environment_qualification_postgres_receipts DROP CONSTRAINT IF EXISTS environment_qualification_postgres_receipts_access_v2_check;

-- +goose StatementBegin
DO $replica$
DECLARE definition text;
BEGIN
  SELECT pg_get_functiondef('environment_runtime_inputs_fresh(uuid,text,timestamptz,jsonb,jsonb,boolean,jsonb,jsonb)'::regprocedure) INTO definition;
  IF position($$b.access IN ('read_write','read_only','data_api','auth','session')$$ IN definition)>0 THEN
    EXECUTE replace(definition, $$b.access IN ('read_write','read_only','data_api','auth','session')$$, $$b.access IN ('read_write','read_only','data_api','auth','session','replica_only')$$);
  ELSIF position($$b.access IN ('read_write','read_only','data_api','auth','session','replica_only')$$ IN definition)=0 THEN
    RAISE EXCEPTION 'unexpected credential policy in environment_runtime_inputs_fresh';
  END IF;
  SELECT pg_get_functiondef('environment_binding_postgres_deadline(environment_workload_qualification_requests,text,bigint,uuid)'::regprocedure) INTO definition;
  IF position($$b.access NOT IN ('read_write','read_only','migration','session')$$ IN definition)>0 THEN
    EXECUTE replace(definition, $$b.access NOT IN ('read_write','read_only','migration','session')$$, $$b.access NOT IN ('read_write','read_only','migration','session','replica_only')$$);
  ELSIF position($$b.access NOT IN ('read_write','read_only','migration','session','replica_only')$$ IN definition)=0 THEN
    RAISE EXCEPTION 'unexpected credential policy in environment_binding_postgres_deadline';
  END IF;
  SELECT pg_get_functiondef('data_pipeline_target_guard()'::regprocedure) INTO definition;
  IF position($$NEW.access<>'read_only'$$ IN definition)>0 THEN
    EXECUTE replace(definition, $$NEW.access<>'read_only'$$, $$NEW.access NOT IN ('read_only','replica_only')$$);
  ELSIF position($$NEW.access NOT IN ('read_only','replica_only')$$ IN definition)=0 THEN
    RAISE EXCEPTION 'unexpected credential policy in data_pipeline_target_guard';
  END IF;
END
$replica$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $replica$
DECLARE definition text;
BEGIN
  SET LOCAL lock_timeout = '5s';
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'managed_postgres_bindings'::regclass AND conname = 'managed_postgres_bindings_access_v2_check') THEN
    ALTER TABLE managed_postgres_bindings ADD CONSTRAINT managed_postgres_bindings_access_v2_check
      CHECK (access IN ('read_write','read_only','migration','data_api','auth','session')) NOT VALID;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'managed_postgres_cutover_credentials'::regclass AND conname = 'managed_postgres_cutover_credentials_access_v2_check') THEN
    ALTER TABLE managed_postgres_cutover_credentials ADD CONSTRAINT managed_postgres_cutover_credentials_access_v2_check
      CHECK (access IN ('read_write','read_only','migration','data_api','auth','session')) NOT VALID;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'environment_qualification_postgres_receipts'::regclass AND conname = 'environment_qualification_postgres_receipts_access_v2_check') THEN
    ALTER TABLE environment_qualification_postgres_receipts ADD CONSTRAINT environment_qualification_postgres_receipts_access_v2_check
      CHECK (access IN ('read_write','read_only','migration','session')) NOT VALID;
  END IF;
  ALTER TABLE managed_postgres_bindings VALIDATE CONSTRAINT managed_postgres_bindings_access_v2_check;
  ALTER TABLE managed_postgres_cutover_credentials VALIDATE CONSTRAINT managed_postgres_cutover_credentials_access_v2_check;
  ALTER TABLE environment_qualification_postgres_receipts VALIDATE CONSTRAINT environment_qualification_postgres_receipts_access_v2_check;

  ALTER TABLE managed_postgres_bindings DROP CONSTRAINT IF EXISTS managed_postgres_bindings_access_v3_check;
  ALTER TABLE managed_postgres_cutover_credentials DROP CONSTRAINT IF EXISTS managed_postgres_cutover_credentials_access_v3_check;
  ALTER TABLE environment_qualification_postgres_receipts DROP CONSTRAINT IF EXISTS environment_qualification_postgres_receipts_access_v3_check;

  SELECT pg_get_functiondef('environment_runtime_inputs_fresh(uuid,text,timestamptz,jsonb,jsonb,boolean,jsonb,jsonb)'::regprocedure) INTO definition;
  IF position($$b.access IN ('read_write','read_only','data_api','auth','session','replica_only')$$ IN definition)>0 THEN
    EXECUTE replace(definition, $$b.access IN ('read_write','read_only','data_api','auth','session','replica_only')$$, $$b.access IN ('read_write','read_only','data_api','auth','session')$$);
  ELSIF position($$b.access IN ('read_write','read_only','data_api','auth','session')$$ IN definition)=0 THEN
    RAISE EXCEPTION 'unexpected credential policy in environment_runtime_inputs_fresh';
  END IF;
  SELECT pg_get_functiondef('environment_binding_postgres_deadline(environment_workload_qualification_requests,text,bigint,uuid)'::regprocedure) INTO definition;
  IF position($$b.access NOT IN ('read_write','read_only','migration','session','replica_only')$$ IN definition)>0 THEN
    EXECUTE replace(definition, $$b.access NOT IN ('read_write','read_only','migration','session','replica_only')$$, $$b.access NOT IN ('read_write','read_only','migration','session')$$);
  ELSIF position($$b.access NOT IN ('read_write','read_only','migration','session')$$ IN definition)=0 THEN
    RAISE EXCEPTION 'unexpected credential policy in environment_binding_postgres_deadline';
  END IF;
  SELECT pg_get_functiondef('data_pipeline_target_guard()'::regprocedure) INTO definition;
  IF position($$NEW.access NOT IN ('read_only','replica_only')$$ IN definition)>0 THEN
    EXECUTE replace(definition, $$NEW.access NOT IN ('read_only','replica_only')$$, $$NEW.access<>'read_only'$$);
  ELSIF position($$NEW.access<>'read_only'$$ IN definition)=0 THEN
    RAISE EXCEPTION 'unexpected credential policy in data_pipeline_target_guard';
  END IF;
END
$replica$;
-- +goose StatementEnd
