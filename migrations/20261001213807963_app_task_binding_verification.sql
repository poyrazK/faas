-- filename: 20261001213807963_app_task_binding_verification.sql

-- +goose Up
-- +goose StatementBegin
ALTER TABLE app_tasks ADD COLUMN IF NOT EXISTS binding_verification jsonb;
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='app_tasks'::regclass AND conname='app_tasks_binding_verification_check') THEN
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
 END IF;
END $$;
CREATE INDEX IF NOT EXISTS app_tasks_binding_verification_latest_idx ON app_tasks
 (account_id, app_id, (binding_verification->>'type'), (binding_verification->>'binding'), deployment_scope, created_at DESC, id DESC)
 WHERE binding_verification IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX app_tasks_binding_verification_latest_idx;
ALTER TABLE app_tasks DROP CONSTRAINT app_tasks_binding_verification_check;
ALTER TABLE app_tasks DROP COLUMN binding_verification;
-- +goose StatementEnd
