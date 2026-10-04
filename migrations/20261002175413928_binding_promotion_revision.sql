-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS app_binding_promotion_revisions (
 app_id uuid PRIMARY KEY REFERENCES apps(id) ON DELETE CASCADE,
 epoch uuid NOT NULL DEFAULT gen_random_uuid(),
 revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0)
);
INSERT INTO app_binding_promotion_revisions(app_id) SELECT id FROM apps ON CONFLICT (app_id) DO NOTHING;

CREATE OR REPLACE FUNCTION bump_binding_promotion_revision(target uuid) RETURNS void
LANGUAGE plpgsql AS $$
BEGIN
 INSERT INTO app_binding_promotion_revisions(app_id)
 SELECT id FROM apps WHERE id=target
 ON CONFLICT(app_id) DO UPDATE SET revision=app_binding_promotion_revisions.revision+1;
END $$;

-- Revision increments commit in the same transaction as the observed facts.
-- Lease/usage/heartbeat fields outside inventory's policy are deliberately omitted.
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

DROP TRIGGER IF EXISTS binding_promotion_revision ON apps;
CREATE TRIGGER binding_promotion_revision AFTER INSERT OR UPDATE OR DELETE ON apps
 FOR EACH ROW EXECUTE FUNCTION capture_binding_promotion_revision('id,account_id,slug,status,manifest,type');
DROP TRIGGER IF EXISTS binding_promotion_revision ON accounts;
CREATE TRIGGER binding_promotion_revision AFTER UPDATE ON accounts
 FOR EACH ROW EXECUTE FUNCTION capture_binding_promotion_revision('id,plan');
DROP TRIGGER IF EXISTS binding_promotion_revision ON app_envs;
CREATE TRIGGER binding_promotion_revision AFTER INSERT OR UPDATE OR DELETE ON app_envs
 FOR EACH ROW EXECUTE FUNCTION capture_binding_promotion_revision('account_id,app_id,scope,key,value');
DROP TRIGGER IF EXISTS binding_promotion_revision ON app_secrets;
CREATE TRIGGER binding_promotion_revision AFTER INSERT OR UPDATE OR DELETE ON app_secrets
 FOR EACH ROW EXECUTE FUNCTION capture_binding_promotion_revision('account_id,app_id,scope,key,ciphertext,kid,value_hash,managed_postgres_binding_id,managed_credential_ref,managed_credential_generation,managed_object_storage_credential_id,delivery_version,secret_class');
DROP TRIGGER IF EXISTS binding_promotion_revision ON app_runtime_config_changes;
CREATE TRIGGER binding_promotion_revision AFTER INSERT OR UPDATE OR DELETE ON app_runtime_config_changes
 FOR EACH ROW EXECUTE FUNCTION capture_binding_promotion_revision('app_id,changed_at');
DROP TRIGGER IF EXISTS binding_promotion_revision ON managed_postgres_bindings;
CREATE TRIGGER binding_promotion_revision AFTER INSERT OR UPDATE OR DELETE ON managed_postgres_bindings
 FOR EACH ROW EXECUTE FUNCTION capture_binding_promotion_revision('id,account_id,app_id,database_id,scope,environment_key,access,state,credential_generation,credential_ref,rotation_previous_generation,rotation_wake_id,rotation_cleanup_ready');
DROP TRIGGER IF EXISTS binding_promotion_revision ON managed_postgres_databases;
CREATE TRIGGER binding_promotion_revision AFTER INSERT OR UPDATE OR DELETE ON managed_postgres_databases
 FOR EACH ROW EXECUTE FUNCTION capture_binding_promotion_revision('id,account_id,name,state');
DROP TRIGGER IF EXISTS binding_promotion_revision ON object_buckets;
CREATE TRIGGER binding_promotion_revision AFTER INSERT OR UPDATE OR DELETE ON object_buckets
 FOR EACH ROW EXECUTE FUNCTION capture_binding_promotion_revision('id,account_id,app_id,name,state');
DROP TRIGGER IF EXISTS binding_promotion_revision ON object_storage_s3_credentials;
CREATE TRIGGER binding_promotion_revision AFTER INSERT OR UPDATE OR DELETE ON object_storage_s3_credentials
 FOR EACH ROW EXECUTE FUNCTION capture_binding_promotion_revision('id,account_id,bucket_id,permission,status,managed_app_id,managed_scope,managed_prefix,rotation_parent_id,rotation_wake_id,secret_sealed,kid,created_at');
DROP TRIGGER IF EXISTS binding_promotion_revision ON queue_bindings;
CREATE TRIGGER binding_promotion_revision AFTER INSERT OR UPDATE OR DELETE ON queue_bindings
 FOR EACH ROW EXECUTE FUNCTION capture_binding_promotion_revision('id,account_id,app_id,name,queue_name,mode,enabled');
DROP TRIGGER IF EXISTS binding_promotion_revision ON triggers;
CREATE TRIGGER binding_promotion_revision AFTER INSERT OR UPDATE OR DELETE ON triggers
 FOR EACH ROW EXECUTE FUNCTION capture_binding_promotion_revision('id,app_id,kind,source,config,enabled,created_at');
DROP TRIGGER IF EXISTS binding_promotion_revision ON trigger_consumer_health;
CREATE TRIGGER binding_promotion_revision AFTER INSERT OR UPDATE OR DELETE ON trigger_consumer_health
 FOR EACH ROW EXECUTE FUNCTION capture_binding_promotion_revision('trigger_id,last_poll_at,last_success_at,last_error_at');
DROP TRIGGER IF EXISTS binding_promotion_revision ON outbound_app_bindings;
CREATE TRIGGER binding_promotion_revision AFTER INSERT OR UPDATE OR DELETE ON outbound_app_bindings
 FOR EACH ROW EXECUTE FUNCTION capture_binding_promotion_revision('app_id,account_id,integration_id,allowed_methods,allowed_path_prefixes,daily_request_limit');
DROP TRIGGER IF EXISTS binding_promotion_revision ON outbound_integrations;
CREATE TRIGGER binding_promotion_revision AFTER INSERT OR UPDATE OR DELETE ON outbound_integrations
 FOR EACH ROW EXECUTE FUNCTION capture_binding_promotion_revision('id,account_id,name,enabled,allowed_methods,allowed_path_prefixes,credential_source,origin,provider_auth_mode,owner_kind,token_hash,daily_request_limit');
DROP TRIGGER IF EXISTS binding_promotion_revision ON outbound_integration_credentials;
CREATE TRIGGER binding_promotion_revision AFTER INSERT OR UPDATE OR DELETE ON outbound_integration_credentials
 FOR EACH ROW EXECUTE FUNCTION capture_binding_promotion_revision('integration_id,account_id,authorization_sealed');
DROP TRIGGER IF EXISTS binding_promotion_revision ON app_tasks;
CREATE TRIGGER binding_promotion_revision AFTER INSERT OR UPDATE OR DELETE ON app_tasks
 FOR EACH ROW EXECUTE FUNCTION capture_binding_promotion_revision('id,app_id,account_id,deployment_id,deployment_scope,binding_verification,status,created_at,finished_at,stdout_tail,output_truncated,exit_code');
DROP TRIGGER IF EXISTS binding_promotion_revision ON instances;
CREATE TRIGGER binding_promotion_revision AFTER INSERT OR UPDATE OR DELETE ON instances
 FOR EACH ROW EXECUTE FUNCTION capture_binding_promotion_revision('id,app_id,deployment_id,kind,mode,state,started_at');
DROP TRIGGER IF EXISTS binding_promotion_revision ON deployments;
CREATE TRIGGER binding_promotion_revision AFTER INSERT OR UPDATE OR DELETE ON deployments
 FOR EACH ROW EXECUTE FUNCTION capture_binding_promotion_revision('id,app_id,status,scope,rootfs_key,image_digest,traffic_percent,canary_total_steps,rollout_state');
DROP TRIGGER IF EXISTS binding_promotion_revision ON notification_outbox;
CREATE TRIGGER binding_promotion_revision AFTER INSERT OR UPDATE OR DELETE ON notification_outbox
 FOR EACH ROW EXECUTE FUNCTION capture_binding_promotion_revision('channel,payload,state,attempts,created_at,delivered_at,last_error');
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ DECLARE tab text; BEGIN
 FOREACH tab IN ARRAY ARRAY['apps','accounts','app_envs','app_secrets','app_runtime_config_changes','managed_postgres_bindings','managed_postgres_databases','object_buckets','object_storage_s3_credentials','queue_bindings','triggers','trigger_consumer_health','outbound_app_bindings','outbound_integrations','outbound_integration_credentials','app_tasks','instances','deployments','notification_outbox'] LOOP
  EXECUTE format('DROP TRIGGER binding_promotion_revision ON %I',tab);
 END LOOP;
END $$;
DROP FUNCTION capture_binding_promotion_revision();
DROP FUNCTION bump_binding_promotion_revision(uuid);
DROP TABLE app_binding_promotion_revisions;
-- +goose StatementEnd
