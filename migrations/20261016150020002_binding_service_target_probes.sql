-- +goose Up
ALTER TABLE app_tasks DROP CONSTRAINT app_tasks_binding_verification_check;
ALTER TABLE app_tasks ADD CONSTRAINT app_tasks_binding_verification_check CHECK ((((binding_verification IS NULL) OR COALESCE(((jsonb_typeof(binding_verification) = 'object'::text) AND (binding_verification ?& ARRAY['type'::text, 'binding'::text, 'revision'::text]) AND ((binding_verification ->> 'type'::text) = ANY (ARRAY['service'::text, 'postgres'::text, 'object_storage'::text, 'outbound'::text])) AND ((NOT (binding_verification ? 'object_storage_mode'::text)) OR ((binding_verification ->> 'object_storage_mode'::text) = ''::text) OR (((binding_verification ->> 'type'::text) = 'object_storage'::text) AND ((binding_verification ->> 'object_storage_mode'::text) = ANY (ARRAY['write'::text, 'read_write'::text])) AND ((binding_verification ->> 'object_storage_mode'::text) = command[3]))) AND ((binding_verification ->> 'binding'::text) = command[2]) AND ((binding_verification ->> 'revision'::text) ~ '^[a-f0-9]{64}$'::text) AND (cardinality(command) =
CASE
    WHEN (((binding_verification ->> 'type'::text) = 'outbound'::text) OR ((binding_verification ->> 'object_storage_mode'::text) = ANY (ARRAY['write'::text, 'read_write'::text])) OR coalesce(binding_verification->>'target_deployment_id','') <> '') THEN 3
    ELSE 2
END) AND (NOT command_shell) AND (kind = 'manual'::text) AND (command[1] =
CASE (binding_verification ->> 'type'::text)
    WHEN 'service'::text THEN CASE WHEN coalesce(binding_verification->>'target_deployment_id','') <> '' THEN '__gregale_service_binding_probe_v2__' ELSE '__gregale_service_binding_probe_v1__' END
    WHEN 'postgres'::text THEN '__gregale_postgres_binding_probe_v1__'::text
    WHEN 'object_storage'::text THEN
    CASE
        WHEN ((binding_verification ->> 'object_storage_mode'::text) = ANY (ARRAY['write'::text, 'read_write'::text])) THEN '__gregale_object_storage_binding_probe_v2__'::text
        ELSE '__gregale_object_storage_binding_probe_v1__'::text
    END
    ELSE '__gregale_outbound_binding_probe_v1__'::text
END)), false))) AND (binding_verification IS NULL OR coalesce(binding_verification->>'target_deployment_id','') = '' OR coalesce((binding_verification->>'type') = 'service' AND (binding_verification->>'target_deployment_id') ~ '^[a-f0-9]{8}(-[a-f0-9]{4}){3}-[a-f0-9]{12}$' AND binding_verification->>'target_deployment_id' = command[3],false)));

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN IF EXISTS (SELECT 1 FROM app_tasks WHERE coalesce(binding_verification->>'target_deployment_id','') <> '') THEN RAISE EXCEPTION 'target service probe history prevents downgrade'; END IF; END $$;
-- +goose StatementEnd
ALTER TABLE app_tasks DROP CONSTRAINT app_tasks_binding_verification_check;
ALTER TABLE app_tasks ADD CONSTRAINT app_tasks_binding_verification_check CHECK (((binding_verification IS NULL) OR COALESCE(((jsonb_typeof(binding_verification) = 'object'::text) AND (binding_verification ?& ARRAY['type'::text, 'binding'::text, 'revision'::text]) AND ((binding_verification ->> 'type'::text) = ANY (ARRAY['service'::text, 'postgres'::text, 'object_storage'::text, 'outbound'::text])) AND ((NOT (binding_verification ? 'object_storage_mode'::text)) OR ((binding_verification ->> 'object_storage_mode'::text) = ''::text) OR (((binding_verification ->> 'type'::text) = 'object_storage'::text) AND ((binding_verification ->> 'object_storage_mode'::text) = ANY (ARRAY['write'::text, 'read_write'::text])) AND ((binding_verification ->> 'object_storage_mode'::text) = command[3]))) AND ((binding_verification ->> 'binding'::text) = command[2]) AND ((binding_verification ->> 'revision'::text) ~ '^[a-f0-9]{64}$'::text) AND (cardinality(command) =
CASE
    WHEN (((binding_verification ->> 'type'::text) = 'outbound'::text) OR ((binding_verification ->> 'object_storage_mode'::text) = ANY (ARRAY['write'::text, 'read_write'::text]))) THEN 3
    ELSE 2
END) AND (NOT command_shell) AND (kind = 'manual'::text) AND (command[1] =
CASE (binding_verification ->> 'type'::text)
    WHEN 'service'::text THEN '__gregale_service_binding_probe_v1__'::text
    WHEN 'postgres'::text THEN '__gregale_postgres_binding_probe_v1__'::text
    WHEN 'object_storage'::text THEN
    CASE
        WHEN ((binding_verification ->> 'object_storage_mode'::text) = ANY (ARRAY['write'::text, 'read_write'::text])) THEN '__gregale_object_storage_binding_probe_v2__'::text
        ELSE '__gregale_object_storage_binding_probe_v1__'::text
    END
    ELSE '__gregale_outbound_binding_probe_v1__'::text
END)), false)));
