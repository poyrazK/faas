-- filename: 20261109191233945_managed_postgres_session_access.sql
-- migration-compat: expand
-- ADR-20261109180000: `session` bindings are serving credentials with
-- read_write privileges on the direct endpoint, for code that needs session
-- features a transaction pooler cannot keep. They reach serving instances
-- like read_write, so runtime freshness counts them.
-- The widened CHECKs are added under new names NOT VALID and validated
-- separately (SHARE UPDATE EXCLUSIVE) before the old ones are dropped, so the
-- tables keep being written. Every step is re-runnable.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';

-- +goose StatementBegin
DO $session$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'managed_postgres_bindings'::regclass AND conname = 'managed_postgres_bindings_access_v2_check') THEN
    ALTER TABLE managed_postgres_bindings ADD CONSTRAINT managed_postgres_bindings_access_v2_check
      CHECK (access IN ('read_write','read_only','migration','data_api','auth','session')) NOT VALID;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'managed_postgres_cutover_credentials'::regclass AND conname = 'managed_postgres_cutover_credentials_access_v2_check') THEN
    ALTER TABLE managed_postgres_cutover_credentials ADD CONSTRAINT managed_postgres_cutover_credentials_access_v2_check
      CHECK (access IN ('read_write','read_only','migration','data_api','auth','session')) NOT VALID;
  END IF;
END
$session$;
-- +goose StatementEnd

ALTER TABLE managed_postgres_bindings VALIDATE CONSTRAINT managed_postgres_bindings_access_v2_check;
ALTER TABLE managed_postgres_cutover_credentials VALIDATE CONSTRAINT managed_postgres_cutover_credentials_access_v2_check;
ALTER TABLE managed_postgres_bindings DROP CONSTRAINT IF EXISTS managed_postgres_bindings_access_check;
ALTER TABLE managed_postgres_cutover_credentials DROP CONSTRAINT IF EXISTS managed_postgres_cutover_credentials_access_check;

-- +goose StatementBegin
DO $session$
DECLARE definition text;
BEGIN
  SELECT pg_get_functiondef('environment_runtime_inputs_fresh(uuid,text,timestamptz,jsonb,jsonb,boolean,jsonb,jsonb)'::regprocedure) INTO definition;
  IF position($$b.access IN ('read_write','read_only','data_api','auth')$$ IN definition)>0 THEN
    EXECUTE replace(definition, $$b.access IN ('read_write','read_only','data_api','auth')$$, $$b.access IN ('read_write','read_only','data_api','auth','session')$$);
  ELSIF position($$b.access IN ('read_write','read_only','data_api','auth','session')$$ IN definition)=0 THEN
    RAISE EXCEPTION 'runtime input credential policy differs from the expected migration contract';
  END IF;
END
$session$;
-- +goose StatementEnd

-- +goose Down
SET lock_timeout = '5s';
-- These constraints refuse downgrade while any session binding remains.
-- +goose StatementBegin
DO $session$
DECLARE definition text;
BEGIN
  SELECT pg_get_functiondef('environment_runtime_inputs_fresh(uuid,text,timestamptz,jsonb,jsonb,boolean,jsonb,jsonb)'::regprocedure) INTO definition;
  IF position($$b.access IN ('read_write','read_only','data_api','auth','session')$$ IN definition)>0 THEN
    EXECUTE replace(definition, $$b.access IN ('read_write','read_only','data_api','auth','session')$$, $$b.access IN ('read_write','read_only','data_api','auth')$$);
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'managed_postgres_bindings'::regclass AND conname = 'managed_postgres_bindings_access_check') THEN
    ALTER TABLE managed_postgres_bindings ADD CONSTRAINT managed_postgres_bindings_access_check
      CHECK (access IN ('read_write','read_only','migration','data_api','auth'));
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'managed_postgres_cutover_credentials'::regclass AND conname = 'managed_postgres_cutover_credentials_access_check') THEN
    ALTER TABLE managed_postgres_cutover_credentials ADD CONSTRAINT managed_postgres_cutover_credentials_access_check
      CHECK (access IN ('read_write','read_only','migration','data_api','auth'));
  END IF;
END
$session$;
-- +goose StatementEnd
ALTER TABLE managed_postgres_bindings DROP CONSTRAINT IF EXISTS managed_postgres_bindings_access_v2_check;
ALTER TABLE managed_postgres_cutover_credentials DROP CONSTRAINT IF EXISTS managed_postgres_cutover_credentials_access_v2_check;
