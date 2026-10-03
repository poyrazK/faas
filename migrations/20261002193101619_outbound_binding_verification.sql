-- filename: 20261002193101619_outbound_binding_verification.sql

-- +goose Up
-- +goose StatementBegin
DROP TRIGGER binding_promotion_revision ON outbound_integration_credentials;
CREATE TRIGGER binding_promotion_revision AFTER INSERT OR UPDATE OR DELETE ON outbound_integration_credentials
 FOR EACH ROW EXECUTE FUNCTION capture_binding_promotion_revision('integration_id,account_id,authorization_sealed,updated_at');
CREATE TABLE outbound_integration_probe_policies (
 integration_id uuid PRIMARY KEY REFERENCES outbound_integrations(id) ON DELETE CASCADE,
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 method text NOT NULL CHECK (method IN ('GET','HEAD')),
 path text NOT NULL CHECK (length(path) BETWEEN 1 AND 512 AND path ~ '^/[^?#]*$'),
 expected_status integer NOT NULL CHECK (expected_status BETWEEN 200 AND 299)
);
CREATE OR REPLACE FUNCTION capture_binding_promotion_revision() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
 old_row jsonb := CASE WHEN TG_OP='INSERT' THEN '{}'::jsonb ELSE to_jsonb(OLD) END;
 new_row jsonb := CASE WHEN TG_OP='DELETE' THEN '{}'::jsonb ELSE to_jsonb(NEW) END;
 old_facts jsonb;
 new_facts jsonb;
 row_data jsonb;
 target uuid;
 targets uuid[] := '{}';
 watched text[] := string_to_array(TG_ARGV[0],',');
BEGIN
 IF TG_OP='UPDATE' THEN
  SELECT jsonb_object_agg(key,value) INTO old_facts FROM jsonb_each(old_row) WHERE key=ANY(watched);
  SELECT jsonb_object_agg(key,value) INTO new_facts FROM jsonb_each(new_row) WHERE key=ANY(watched);
  IF old_facts IS NOT DISTINCT FROM new_facts THEN RETURN NEW; END IF;
 END IF;
 IF TG_TABLE_NAME='app_tasks' AND old_row->>'binding_verification' IS NULL AND new_row->>'binding_verification' IS NULL THEN
  IF TG_OP='DELETE' THEN RETURN OLD; END IF; RETURN NEW;
 END IF;
 FOR row_data IN SELECT old_row UNION SELECT new_row LOOP
  IF row_data='{}'::jsonb THEN CONTINUE; END IF;
  CASE TG_TABLE_NAME
   WHEN 'apps' THEN targets:=targets||ARRAY[(row_data->>'id')::uuid];
   WHEN 'accounts' THEN
    targets:=targets||ARRAY(SELECT id FROM apps WHERE account_id=(row_data->>'id')::uuid);
   WHEN 'managed_postgres_databases' THEN
    targets:=targets||ARRAY(SELECT app_id FROM managed_postgres_bindings WHERE database_id=(row_data->>'id')::uuid);
   WHEN 'object_storage_s3_credentials' THEN
    targets:=targets||ARRAY[NULLIF(row_data->>'managed_app_id','')::uuid];
    targets:=targets||ARRAY(SELECT managed_app_id FROM object_storage_s3_credentials WHERE id=NULLIF(row_data->>'rotation_parent_id','')::uuid);
   WHEN 'outbound_integration_probe_policies' THEN
    targets:=targets||ARRAY(SELECT app_id FROM outbound_app_bindings WHERE integration_id=(row_data->>'integration_id')::uuid);
   WHEN 'outbound_integrations' THEN
    targets:=targets||ARRAY(SELECT app_id FROM outbound_app_bindings WHERE integration_id=(row_data->>'id')::uuid);
   WHEN 'outbound_integration_credentials' THEN
    targets:=targets||ARRAY(SELECT app_id FROM outbound_app_bindings WHERE integration_id=(row_data->>'integration_id')::uuid);
   WHEN 'trigger_consumer_health' THEN
    targets:=targets||ARRAY(SELECT app_id FROM triggers WHERE id=(row_data->>'trigger_id')::uuid);
   WHEN 'notification_outbox' THEN
    IF row_data->>'channel'='runtime_config_restart' THEN
     BEGIN
      targets:=targets||ARRAY[NULLIF((row_data->>'payload')::jsonb->>'app_id','')::uuid];
     EXCEPTION WHEN invalid_text_representation THEN
      -- Malformed refresh metadata makes inventory unreadable; invalidate all
      -- approvals rather than allowing an old readable projection to survive.
      targets:=targets||ARRAY(SELECT id FROM apps);
     END;
    END IF;
   WHEN 'instances' THEN
    IF row_data->>'kind'='wake' AND row_data->>'mode'<>'mirror' AND
       row_data->>'state' IN ('running','waking','cold_booting','warm','snapshotting','draining','migrating') THEN
     targets:=targets||ARRAY[(row_data->>'app_id')::uuid];
    END IF;
   ELSE targets:=targets||ARRAY[(row_data->>'app_id')::uuid];
  END CASE;
 END LOOP;
 -- Stable ordering also serializes writes that affect multiple bound apps.
 FOR target IN SELECT DISTINCT id FROM unnest(targets) id WHERE id IS NOT NULL ORDER BY id LOOP
  PERFORM bump_binding_promotion_revision(target);
 END LOOP;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF; RETURN NEW;
END $$;
DROP TRIGGER binding_promotion_revision ON outbound_integrations;
CREATE TRIGGER binding_promotion_revision AFTER INSERT OR UPDATE OR DELETE ON outbound_integrations
 FOR EACH ROW EXECUTE FUNCTION capture_binding_promotion_revision('id,account_id,name,enabled,allowed_methods,allowed_path_prefixes,credential_source,origin,provider_auth_mode,owner_kind,token_hash,daily_request_limit,rate_per_second,burst,max_in_flight,request_timeout_ms,max_retries,response_cache_ttl_seconds,circuit_breaker_failure_threshold,circuit_breaker_open_seconds,retry_budget_per_minute');
CREATE TRIGGER binding_promotion_revision AFTER INSERT OR UPDATE OR DELETE ON outbound_integration_probe_policies
 FOR EACH ROW EXECUTE FUNCTION capture_binding_promotion_revision('integration_id,account_id,method,path,expected_status');
ALTER TABLE app_tasks DROP CONSTRAINT app_tasks_binding_verification_check;
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

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER binding_promotion_revision ON outbound_integration_credentials;
CREATE TRIGGER binding_promotion_revision AFTER INSERT OR UPDATE OR DELETE ON outbound_integration_credentials
 FOR EACH ROW EXECUTE FUNCTION capture_binding_promotion_revision('integration_id,account_id,authorization_sealed');
DROP TABLE outbound_integration_probe_policies;
CREATE OR REPLACE FUNCTION capture_binding_promotion_revision() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
 old_row jsonb := CASE WHEN TG_OP='INSERT' THEN '{}'::jsonb ELSE to_jsonb(OLD) END;
 new_row jsonb := CASE WHEN TG_OP='DELETE' THEN '{}'::jsonb ELSE to_jsonb(NEW) END;
 old_facts jsonb;
 new_facts jsonb;
 row_data jsonb;
 target uuid;
 targets uuid[] := '{}';
 watched text[] := string_to_array(TG_ARGV[0],',');
BEGIN
 IF TG_OP='UPDATE' THEN
  SELECT jsonb_object_agg(key,value) INTO old_facts FROM jsonb_each(old_row) WHERE key=ANY(watched);
  SELECT jsonb_object_agg(key,value) INTO new_facts FROM jsonb_each(new_row) WHERE key=ANY(watched);
  IF old_facts IS NOT DISTINCT FROM new_facts THEN RETURN NEW; END IF;
 END IF;
 IF TG_TABLE_NAME='app_tasks' AND old_row->>'binding_verification' IS NULL AND new_row->>'binding_verification' IS NULL THEN
  IF TG_OP='DELETE' THEN RETURN OLD; END IF; RETURN NEW;
 END IF;
 FOR row_data IN SELECT old_row UNION SELECT new_row LOOP
  IF row_data='{}'::jsonb THEN CONTINUE; END IF;
  CASE TG_TABLE_NAME
   WHEN 'apps' THEN targets:=targets||ARRAY[(row_data->>'id')::uuid];
   WHEN 'accounts' THEN
    targets:=targets||ARRAY(SELECT id FROM apps WHERE account_id=(row_data->>'id')::uuid);
   WHEN 'managed_postgres_databases' THEN
    targets:=targets||ARRAY(SELECT app_id FROM managed_postgres_bindings WHERE database_id=(row_data->>'id')::uuid);
   WHEN 'object_storage_s3_credentials' THEN
    targets:=targets||ARRAY[NULLIF(row_data->>'managed_app_id','')::uuid];
    targets:=targets||ARRAY(SELECT managed_app_id FROM object_storage_s3_credentials WHERE id=NULLIF(row_data->>'rotation_parent_id','')::uuid);
   WHEN 'outbound_integrations' THEN
    targets:=targets||ARRAY(SELECT app_id FROM outbound_app_bindings WHERE integration_id=(row_data->>'id')::uuid);
   WHEN 'outbound_integration_credentials' THEN
    targets:=targets||ARRAY(SELECT app_id FROM outbound_app_bindings WHERE integration_id=(row_data->>'integration_id')::uuid);
   WHEN 'trigger_consumer_health' THEN
    targets:=targets||ARRAY(SELECT app_id FROM triggers WHERE id=(row_data->>'trigger_id')::uuid);
   WHEN 'notification_outbox' THEN
    IF row_data->>'channel'='runtime_config_restart' THEN
     BEGIN
      targets:=targets||ARRAY[NULLIF((row_data->>'payload')::jsonb->>'app_id','')::uuid];
     EXCEPTION WHEN invalid_text_representation THEN
      -- Malformed refresh metadata makes inventory unreadable; invalidate all
      -- approvals rather than allowing an old readable projection to survive.
      targets:=targets||ARRAY(SELECT id FROM apps);
     END;
    END IF;
   WHEN 'instances' THEN
    IF row_data->>'kind'='wake' AND row_data->>'mode'<>'mirror' AND
       row_data->>'state' IN ('running','waking','cold_booting','warm','snapshotting','draining','migrating') THEN
     targets:=targets||ARRAY[(row_data->>'app_id')::uuid];
    END IF;
   ELSE targets:=targets||ARRAY[(row_data->>'app_id')::uuid];
  END CASE;
 END LOOP;
 -- Stable ordering also serializes writes that affect multiple bound apps.
 FOR target IN SELECT DISTINCT id FROM unnest(targets) id WHERE id IS NOT NULL ORDER BY id LOOP
  PERFORM bump_binding_promotion_revision(target);
 END LOOP;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF; RETURN NEW;
END $$;
DROP TRIGGER binding_promotion_revision ON outbound_integrations;
CREATE TRIGGER binding_promotion_revision AFTER INSERT OR UPDATE OR DELETE ON outbound_integrations
 FOR EACH ROW EXECUTE FUNCTION capture_binding_promotion_revision('id,account_id,name,enabled,allowed_methods,allowed_path_prefixes,credential_source,origin,provider_auth_mode,owner_kind,token_hash,daily_request_limit');
UPDATE app_tasks SET binding_verification=NULL WHERE binding_verification->>'type'='outbound';
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
