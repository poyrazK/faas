-- filename: 20261002073437738_object_storage_binding_verification.sql

-- +goose Up
-- +goose StatementBegin
CREATE INDEX object_s3_credentials_rotation_revision_idx ON object_storage_s3_credentials
 (rotation_parent_id, account_id, created_at DESC, id DESC) WHERE rotation_parent_id IS NOT NULL;
ALTER TABLE app_tasks DROP CONSTRAINT app_tasks_binding_verification_check;
ALTER TABLE app_tasks ADD CONSTRAINT app_tasks_binding_verification_check CHECK (
 binding_verification IS NULL OR COALESCE((
  jsonb_typeof(binding_verification) = 'object'
  AND binding_verification ?& ARRAY['type', 'binding', 'revision']
  AND binding_verification->>'type' IN ('service', 'postgres', 'object_storage')
  AND binding_verification->>'binding' = command[2]
  AND binding_verification->>'revision' ~ '^[a-f0-9]{64}$'
  AND cardinality(command) = 2 AND NOT command_shell AND kind = 'manual'
  AND command[1] = CASE binding_verification->>'type'
   WHEN 'service' THEN '__gregale_service_binding_probe_v1__'
   WHEN 'postgres' THEN '__gregale_postgres_binding_probe_v1__'
   ELSE '__gregale_object_storage_binding_probe_v1__' END
 ), false)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX object_s3_credentials_rotation_revision_idx;
UPDATE app_tasks SET binding_verification = NULL WHERE binding_verification->>'type' = 'object_storage';
ALTER TABLE app_tasks DROP CONSTRAINT app_tasks_binding_verification_check;
ALTER TABLE app_tasks ADD CONSTRAINT app_tasks_binding_verification_check CHECK (
 binding_verification IS NULL OR COALESCE((
  jsonb_typeof(binding_verification) = 'object'
  AND binding_verification ?& ARRAY['type', 'binding', 'revision']
  AND binding_verification->>'type' IN ('service', 'postgres')
  AND binding_verification->>'binding' = command[2]
  AND binding_verification->>'revision' ~ '^[a-f0-9]{64}$'
  AND cardinality(command) = 2 AND NOT command_shell AND kind = 'manual'
  AND command[1] = CASE binding_verification->>'type'
   WHEN 'service' THEN '__gregale_service_binding_probe_v1__'
   ELSE '__gregale_postgres_binding_probe_v1__' END
 ), false)
);
-- +goose StatementEnd
