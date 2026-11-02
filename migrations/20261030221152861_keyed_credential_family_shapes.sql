-- filename: 20261030221152861_keyed_credential_family_shapes.sql
-- migration-compat: expand
-- ADR 20261029202403 amendment 1: the scheduler, outbound, uploads, attach
-- and dev-bridge credential columns hold either a plain SHA-256 (32 bytes,
-- 64 hex characters) or a keyed digest (33 bytes; 66 hex characters). Each
-- length or shape check is widened online under a new name: added NOT
-- VALID, validated, then the old one dropped, each step committing on its
-- own and guarded so a rerun converges. Writers stay plain until each
-- family's keyed writes are turned on.
--
-- capture_binding_promotion_revision ignores a token_hash change made with
-- faas.credential_rekey set: outboundd sets it only when it rewrites an
-- integration's hash for the same token, so keying or rotating a hash does
-- not bump the promotion revision of every bound app. A new token still
-- does.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';
-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'event_delivery_acknowledgments'::regclass AND conname = 'event_delivery_acknowledgments_token_credhash_chk') THEN
  ALTER TABLE event_delivery_acknowledgments ADD CONSTRAINT event_delivery_acknowledgments_token_credhash_chk CHECK ((octet_length(token_digest) = ANY (ARRAY[32, 33]))) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE event_delivery_acknowledgments VALIDATE CONSTRAINT event_delivery_acknowledgments_token_credhash_chk;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE event_delivery_acknowledgments DROP CONSTRAINT IF EXISTS event_delivery_acknowledgments_token_digest_check;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'ci_job_runtime'::regclass AND conname = 'ci_job_runtime_capability_credhash_chk') THEN
  ALTER TABLE ci_job_runtime ADD CONSTRAINT ci_job_runtime_capability_credhash_chk CHECK ((octet_length(capability_hash) = ANY (ARRAY[32, 33]))) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE ci_job_runtime VALIDATE CONSTRAINT ci_job_runtime_capability_credhash_chk;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE ci_job_runtime DROP CONSTRAINT IF EXISTS ci_job_runtime_capability_hash_check;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'managed_realtime_consumer_group_messages'::regclass AND conname = 'managed_realtime_consumer_group_messages_credhash_chk') THEN
  ALTER TABLE managed_realtime_consumer_group_messages ADD CONSTRAINT managed_realtime_consumer_group_messages_credhash_chk CHECK (((token_hash IS NULL) OR (octet_length(token_hash) = ANY (ARRAY[32, 33])))) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE managed_realtime_consumer_group_messages VALIDATE CONSTRAINT managed_realtime_consumer_group_messages_credhash_chk;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE managed_realtime_consumer_group_messages DROP CONSTRAINT IF EXISTS managed_realtime_consumer_group_messages_token_hash_check;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'outbound_integrations'::regclass AND conname = 'outbound_integrations_token_credhash_chk') THEN
  ALTER TABLE outbound_integrations ADD CONSTRAINT outbound_integrations_token_credhash_chk CHECK ((octet_length(token_hash) = ANY (ARRAY[32, 33]))) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE outbound_integrations VALIDATE CONSTRAINT outbound_integrations_token_credhash_chk;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE outbound_integrations DROP CONSTRAINT IF EXISTS outbound_integrations_token_hash_check;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'outbound_ai_access_tokens'::regclass AND conname = 'outbound_ai_access_tokens_secret_credhash_chk') THEN
  ALTER TABLE outbound_ai_access_tokens ADD CONSTRAINT outbound_ai_access_tokens_secret_credhash_chk CHECK (((octet_length(token_hash) = ANY (ARRAY[32, 33])) AND (token_prefix ~ '^ggai_[A-Za-z0-9_-]{7}$'::text))) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE outbound_ai_access_tokens VALIDATE CONSTRAINT outbound_ai_access_tokens_secret_credhash_chk;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE outbound_ai_access_tokens DROP CONSTRAINT IF EXISTS outbound_ai_access_tokens_secret_chk;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'object_storage_upload_grants'::regclass AND conname = 'object_storage_upload_grants_token_credhash_chk') THEN
  ALTER TABLE object_storage_upload_grants ADD CONSTRAINT object_storage_upload_grants_token_credhash_chk CHECK ((token_hash ~ '^[0-9a-f]{64}([0-9a-f]{2})?$'::text)) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE object_storage_upload_grants VALIDATE CONSTRAINT object_storage_upload_grants_token_credhash_chk;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE object_storage_upload_grants DROP CONSTRAINT IF EXISTS object_storage_upload_grants_token_hash_check;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_task_attach_sessions'::regclass AND conname = 'app_task_attach_sessions_token_credhash_chk') THEN
  ALTER TABLE app_task_attach_sessions ADD CONSTRAINT app_task_attach_sessions_token_credhash_chk CHECK ((octet_length(attach_token_sha256) = ANY (ARRAY[32, 33]))) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE app_task_attach_sessions VALIDATE CONSTRAINT app_task_attach_sessions_token_credhash_chk;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE app_task_attach_sessions DROP CONSTRAINT IF EXISTS app_task_attach_sessions_token_chk;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'job_run_attach_sessions'::regclass AND conname = 'job_run_attach_sessions_token_credhash_chk') THEN
  ALTER TABLE job_run_attach_sessions ADD CONSTRAINT job_run_attach_sessions_token_credhash_chk CHECK ((octet_length(attach_token_sha256) = ANY (ARRAY[32, 33]))) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE job_run_attach_sessions VALIDATE CONSTRAINT job_run_attach_sessions_token_credhash_chk;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE job_run_attach_sessions DROP CONSTRAINT IF EXISTS job_run_attach_sessions_token_chk;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'dev_bridge_sessions'::regclass AND conname = 'dev_bridge_sessions_attachment_credhash_chk') THEN
  ALTER TABLE dev_bridge_sessions ADD CONSTRAINT dev_bridge_sessions_attachment_credhash_chk CHECK ((octet_length(attachment_digest) = ANY (ARRAY[32, 33]))) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE dev_bridge_sessions VALIDATE CONSTRAINT dev_bridge_sessions_attachment_credhash_chk;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE dev_bridge_sessions DROP CONSTRAINT IF EXISTS dev_bridge_sessions_attachment_digest_check;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'dev_bridge_sessions'::regclass AND conname = 'dev_bridge_sessions_request_credhash_chk') THEN
  ALTER TABLE dev_bridge_sessions ADD CONSTRAINT dev_bridge_sessions_request_credhash_chk CHECK ((octet_length(request_digest) = ANY (ARRAY[32, 33]))) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE dev_bridge_sessions VALIDATE CONSTRAINT dev_bridge_sessions_request_credhash_chk;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE dev_bridge_sessions DROP CONSTRAINT IF EXISTS dev_bridge_sessions_request_digest_check;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.capture_binding_promotion_revision() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
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
 -- ADR 20261029202403 amendment 1: rewriting a credential hash for the same
 -- token (plain to keyed, or a key rotation) is not a binding change.
 IF current_setting('faas.credential_rekey', true) = 'on' THEN
  watched := array_remove(watched, 'token_hash');
 END IF;
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
       row_data->>'state' IN ('running','waking','cold_booting','warm','frozen','snapshotting','draining','migrating') THEN
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
-- +goose StatementEnd

-- +goose Down
SET lock_timeout = '5s';
-- Rolling back needs every keyed value gone: the old checks fail to
-- validate while one remains.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.capture_binding_promotion_revision() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
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
       row_data->>'state' IN ('running','waking','cold_booting','warm','frozen','snapshotting','draining','migrating') THEN
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
-- +goose StatementEnd

-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'event_delivery_acknowledgments'::regclass AND conname = 'event_delivery_acknowledgments_token_digest_check') THEN
  ALTER TABLE event_delivery_acknowledgments ADD CONSTRAINT event_delivery_acknowledgments_token_digest_check CHECK ((octet_length(token_digest) = 32)) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE event_delivery_acknowledgments VALIDATE CONSTRAINT event_delivery_acknowledgments_token_digest_check;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE event_delivery_acknowledgments DROP CONSTRAINT IF EXISTS event_delivery_acknowledgments_token_credhash_chk;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'ci_job_runtime'::regclass AND conname = 'ci_job_runtime_capability_hash_check') THEN
  ALTER TABLE ci_job_runtime ADD CONSTRAINT ci_job_runtime_capability_hash_check CHECK ((octet_length(capability_hash) = 32)) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE ci_job_runtime VALIDATE CONSTRAINT ci_job_runtime_capability_hash_check;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE ci_job_runtime DROP CONSTRAINT IF EXISTS ci_job_runtime_capability_credhash_chk;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'managed_realtime_consumer_group_messages'::regclass AND conname = 'managed_realtime_consumer_group_messages_token_hash_check') THEN
  ALTER TABLE managed_realtime_consumer_group_messages ADD CONSTRAINT managed_realtime_consumer_group_messages_token_hash_check CHECK (((token_hash IS NULL) OR (octet_length(token_hash) = 32))) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE managed_realtime_consumer_group_messages VALIDATE CONSTRAINT managed_realtime_consumer_group_messages_token_hash_check;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE managed_realtime_consumer_group_messages DROP CONSTRAINT IF EXISTS managed_realtime_consumer_group_messages_credhash_chk;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'outbound_integrations'::regclass AND conname = 'outbound_integrations_token_hash_check') THEN
  ALTER TABLE outbound_integrations ADD CONSTRAINT outbound_integrations_token_hash_check CHECK ((octet_length(token_hash) = 32)) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE outbound_integrations VALIDATE CONSTRAINT outbound_integrations_token_hash_check;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE outbound_integrations DROP CONSTRAINT IF EXISTS outbound_integrations_token_credhash_chk;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'outbound_ai_access_tokens'::regclass AND conname = 'outbound_ai_access_tokens_secret_chk') THEN
  ALTER TABLE outbound_ai_access_tokens ADD CONSTRAINT outbound_ai_access_tokens_secret_chk CHECK (((octet_length(token_hash) = 32) AND (token_prefix ~ '^ggai_[A-Za-z0-9_-]{7}$'::text))) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE outbound_ai_access_tokens VALIDATE CONSTRAINT outbound_ai_access_tokens_secret_chk;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE outbound_ai_access_tokens DROP CONSTRAINT IF EXISTS outbound_ai_access_tokens_secret_credhash_chk;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'object_storage_upload_grants'::regclass AND conname = 'object_storage_upload_grants_token_hash_check') THEN
  ALTER TABLE object_storage_upload_grants ADD CONSTRAINT object_storage_upload_grants_token_hash_check CHECK ((token_hash ~ '^[0-9a-f]{64}$'::text)) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE object_storage_upload_grants VALIDATE CONSTRAINT object_storage_upload_grants_token_hash_check;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE object_storage_upload_grants DROP CONSTRAINT IF EXISTS object_storage_upload_grants_token_credhash_chk;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_task_attach_sessions'::regclass AND conname = 'app_task_attach_sessions_token_chk') THEN
  ALTER TABLE app_task_attach_sessions ADD CONSTRAINT app_task_attach_sessions_token_chk CHECK ((octet_length(attach_token_sha256) = 32)) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE app_task_attach_sessions VALIDATE CONSTRAINT app_task_attach_sessions_token_chk;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE app_task_attach_sessions DROP CONSTRAINT IF EXISTS app_task_attach_sessions_token_credhash_chk;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'job_run_attach_sessions'::regclass AND conname = 'job_run_attach_sessions_token_chk') THEN
  ALTER TABLE job_run_attach_sessions ADD CONSTRAINT job_run_attach_sessions_token_chk CHECK ((octet_length(attach_token_sha256) = 32)) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE job_run_attach_sessions VALIDATE CONSTRAINT job_run_attach_sessions_token_chk;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE job_run_attach_sessions DROP CONSTRAINT IF EXISTS job_run_attach_sessions_token_credhash_chk;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'dev_bridge_sessions'::regclass AND conname = 'dev_bridge_sessions_attachment_digest_check') THEN
  ALTER TABLE dev_bridge_sessions ADD CONSTRAINT dev_bridge_sessions_attachment_digest_check CHECK ((octet_length(attachment_digest) = 32)) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE dev_bridge_sessions VALIDATE CONSTRAINT dev_bridge_sessions_attachment_digest_check;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE dev_bridge_sessions DROP CONSTRAINT IF EXISTS dev_bridge_sessions_attachment_credhash_chk;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'dev_bridge_sessions'::regclass AND conname = 'dev_bridge_sessions_request_digest_check') THEN
  ALTER TABLE dev_bridge_sessions ADD CONSTRAINT dev_bridge_sessions_request_digest_check CHECK ((octet_length(request_digest) = 32)) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE dev_bridge_sessions VALIDATE CONSTRAINT dev_bridge_sessions_request_digest_check;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE dev_bridge_sessions DROP CONSTRAINT IF EXISTS dev_bridge_sessions_request_credhash_chk;
-- +goose StatementEnd
