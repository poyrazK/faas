-- +goose Up
-- +goose StatementBegin
ALTER TABLE managed_postgres_bindings DROP CONSTRAINT managed_postgres_bindings_access_check;
ALTER TABLE managed_postgres_bindings ADD CONSTRAINT managed_postgres_bindings_access_check
 CHECK (access IN ('read_write','read_only','migration','data_api'));
ALTER TABLE managed_postgres_cutover_credentials DROP CONSTRAINT managed_postgres_cutover_credentials_access_check;
ALTER TABLE managed_postgres_cutover_credentials ADD CONSTRAINT managed_postgres_cutover_credentials_access_check
 CHECK (access IN ('read_write','read_only','migration','data_api'));
DO $dataapi$
DECLARE definition text;
BEGIN
 SELECT pg_get_functiondef('environment_runtime_inputs_fresh(uuid,text,timestamptz,jsonb,jsonb,boolean,jsonb,jsonb)'::regprocedure) INTO definition;
 IF position($$b.access IN ('read_write','read_only')$$ IN definition)=0 THEN
  RAISE EXCEPTION 'runtime input credential policy differs from the expected migration contract';
 END IF;
 EXECUTE replace(definition, $$b.access IN ('read_write','read_only')$$, $$b.access IN ('read_write','read_only','data_api')$$);
END;
$dataapi$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- These constraints refuse downgrade while any data API binding remains.
ALTER TABLE managed_postgres_bindings DROP CONSTRAINT managed_postgres_bindings_access_check;
ALTER TABLE managed_postgres_bindings ADD CONSTRAINT managed_postgres_bindings_access_check
 CHECK (access IN ('read_write','read_only','migration'));
ALTER TABLE managed_postgres_cutover_credentials DROP CONSTRAINT managed_postgres_cutover_credentials_access_check;
ALTER TABLE managed_postgres_cutover_credentials ADD CONSTRAINT managed_postgres_cutover_credentials_access_check
 CHECK (access IN ('read_write','read_only','migration'));
DO $dataapi$
DECLARE definition text;
BEGIN
 SELECT pg_get_functiondef('environment_runtime_inputs_fresh(uuid,text,timestamptz,jsonb,jsonb,boolean,jsonb,jsonb)'::regprocedure) INTO definition;
 IF position($$b.access IN ('read_write','read_only','data_api')$$ IN definition)=0 THEN
  RAISE EXCEPTION 'runtime input credential policy differs from the expected migration contract';
 END IF;
 EXECUTE replace(definition, $$b.access IN ('read_write','read_only','data_api')$$, $$b.access IN ('read_write','read_only')$$);
END;
$dataapi$;
-- +goose StatementEnd
