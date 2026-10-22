-- +goose Up
-- +goose StatementBegin
ALTER TABLE app_tasks DROP CONSTRAINT IF EXISTS app_tasks_binding_verification_check;
ALTER TABLE app_tasks ADD CONSTRAINT app_tasks_binding_verification_check CHECK (
 binding_verification IS NULL OR COALESCE((
  jsonb_typeof(binding_verification) = 'object'
  AND binding_verification ?& ARRAY['type', 'binding', 'revision']
  AND binding_verification->>'type' IN ('service', 'postgres', 'object_storage', 'outbound')
  AND (NOT binding_verification ? 'object_storage_mode' OR binding_verification->>'object_storage_mode' = '' OR
       (binding_verification->>'type' = 'object_storage' AND binding_verification->>'object_storage_mode' IN ('write','read_write') AND binding_verification->>'object_storage_mode' = command[3]))
  AND binding_verification->>'binding' = command[2]
  AND binding_verification->>'revision' ~ '^[a-f0-9]{64}$'
  AND cardinality(command) = CASE WHEN binding_verification->>'type'='outbound' OR binding_verification->>'object_storage_mode' IN ('write','read_write') THEN 3 ELSE 2 END AND NOT command_shell AND kind = 'manual'
  AND command[1] = CASE binding_verification->>'type'
   WHEN 'service' THEN '__gregale_service_binding_probe_v1__'
   WHEN 'postgres' THEN '__gregale_postgres_binding_probe_v1__'
   WHEN 'object_storage' THEN CASE WHEN binding_verification->>'object_storage_mode' IN ('write','read_write') THEN '__gregale_object_storage_binding_probe_v2__' ELSE '__gregale_object_storage_binding_probe_v1__' END
   ELSE '__gregale_outbound_binding_probe_v1__' END
 ), false)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Old executors cannot qualify these reports. Remove pins before restoring the old constraint.
UPDATE app_tasks SET binding_verification=NULL WHERE binding_verification->>'object_storage_mode' IN ('write','read_write');
ALTER TABLE app_tasks DROP CONSTRAINT IF EXISTS app_tasks_binding_verification_check;
ALTER TABLE app_tasks ADD CONSTRAINT app_tasks_binding_verification_check CHECK (
 binding_verification IS NULL OR COALESCE((
  jsonb_typeof(binding_verification) = 'object'
  AND binding_verification ?& ARRAY['type', 'binding', 'revision']
  AND binding_verification->>'type' IN ('service', 'postgres', 'object_storage', 'outbound')
  AND binding_verification->>'binding' = command[2]
  AND binding_verification->>'revision' ~ '^[a-f0-9]{64}$'
  AND cardinality(command) = CASE WHEN binding_verification->>'type'='outbound' THEN 3 ELSE 2 END AND NOT command_shell AND kind = 'manual'
  AND command[1] = CASE binding_verification->>'type'
   WHEN 'service' THEN '__gregale_service_binding_probe_v1__'
   WHEN 'postgres' THEN '__gregale_postgres_binding_probe_v1__'
   WHEN 'object_storage' THEN '__gregale_object_storage_binding_probe_v1__'
   ELSE '__gregale_outbound_binding_probe_v1__' END
 ), false)
);
-- +goose StatementEnd
