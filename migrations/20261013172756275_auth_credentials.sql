-- filename: 20261013172756275_auth_credentials.sql

-- ADR-1190: auth bindings are serving credentials for the application Auth
-- app. They reach runtime and release tasks, so runtime freshness counts them.
-- +goose Up
-- +goose StatementBegin
ALTER TABLE managed_postgres_bindings DROP CONSTRAINT managed_postgres_bindings_access_check;
ALTER TABLE managed_postgres_bindings ADD CONSTRAINT managed_postgres_bindings_access_check
 CHECK (access IN ('read_write','read_only','migration','data_api','auth'));
ALTER TABLE managed_postgres_cutover_credentials DROP CONSTRAINT managed_postgres_cutover_credentials_access_check;
ALTER TABLE managed_postgres_cutover_credentials ADD CONSTRAINT managed_postgres_cutover_credentials_access_check
 CHECK (access IN ('read_write','read_only','migration','data_api','auth'));
DO $auth$
DECLARE definition text;
BEGIN
 SELECT pg_get_functiondef('environment_runtime_inputs_fresh(uuid,text,timestamptz,jsonb,jsonb,boolean,jsonb,jsonb)'::regprocedure) INTO definition;
 IF position($$b.access IN ('read_write','read_only','data_api')$$ IN definition)>0 THEN
  EXECUTE replace(definition, $$b.access IN ('read_write','read_only','data_api')$$, $$b.access IN ('read_write','read_only','data_api','auth')$$);
 ELSIF position($$b.access IN ('read_write','read_only','data_api','auth')$$ IN definition)=0 THEN
  RAISE EXCEPTION 'runtime input credential policy differs from the expected migration contract';
 END IF;
END;
$auth$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- These constraints refuse downgrade while any auth binding remains.
ALTER TABLE managed_postgres_bindings DROP CONSTRAINT managed_postgres_bindings_access_check;
ALTER TABLE managed_postgres_bindings ADD CONSTRAINT managed_postgres_bindings_access_check
 CHECK (access IN ('read_write','read_only','migration','data_api'));
ALTER TABLE managed_postgres_cutover_credentials DROP CONSTRAINT managed_postgres_cutover_credentials_access_check;
ALTER TABLE managed_postgres_cutover_credentials ADD CONSTRAINT managed_postgres_cutover_credentials_access_check
 CHECK (access IN ('read_write','read_only','migration','data_api'));
DO $auth$
DECLARE definition text;
BEGIN
 SELECT pg_get_functiondef('environment_runtime_inputs_fresh(uuid,text,timestamptz,jsonb,jsonb,boolean,jsonb,jsonb)'::regprocedure) INTO definition;
 IF position($$b.access IN ('read_write','read_only','data_api','auth')$$ IN definition)>0 THEN
  EXECUTE replace(definition, $$b.access IN ('read_write','read_only','data_api','auth')$$, $$b.access IN ('read_write','read_only','data_api')$$);
 ELSIF position($$b.access IN ('read_write','read_only','data_api')$$ IN definition)=0 THEN
  RAISE EXCEPTION 'runtime input credential policy differs from the expected migration contract';
 END IF;
END;
$auth$;
-- +goose StatementEnd
