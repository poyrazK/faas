-- +goose Up
-- The final schema object marks this transactional Up as applied. A ledger
-- replay must preserve definitions widened by later migrations and must not
-- re-run grant backfills or replace saved rollback constraint definitions.
-- +goose StatementBegin
DO $mcp_replay_guard$ BEGIN
 IF EXISTS(SELECT 1 FROM pg_trigger WHERE tgrelid='apps'::regclass AND tgname='mcp_oauth_aa_renewal_retirement') THEN RETURN; END IF;
CREATE FUNCTION mcp_catalog_oauth_renewal_valid(payload jsonb) RETURNS boolean
LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE view jsonb; journal jsonb; result jsonb;
BEGIN
 FOR view IN SELECT value FROM jsonb_each(jsonb_build_object('default',payload)||COALESCE(payload->'roles','{}'::jsonb)) LOOP
  journal:=view->'observer_status'->'oauth_renewal';
  IF journal IS NULL OR journal='null'::jsonb THEN CONTINUE; END IF;
  IF jsonb_typeof(journal)<>'object' OR COALESCE(journal->>'state','') NOT IN ('pending','saved','installed','failed','reauthorization_required')
   OR COALESCE(journal->>'private_id','') !~ '^[a-f0-9-]{36}$'
   OR COALESCE(journal->>'private_lease_id','') !~ '^[a-f0-9-]{36}$'
   OR COALESCE(journal->>'private_source_sha256','') !~ '^[a-f0-9]{64}$'
   OR journal->>'started_at' IS NULL OR view->'config'->'observer'->'oauth' IS NULL THEN RETURN false; END IF;
  result:=journal->'private_result';
  IF journal->>'state'='saved' THEN
   IF result IS NULL OR result='null'::jsonb OR result->>'source_sha256' IS DISTINCT FROM journal->>'private_source_sha256'
    OR NOT mcp_catalog_observer_oauth_valid(jsonb_build_object('config',view->'config','observer_status',jsonb_build_object('oauth',result))) THEN RETURN false; END IF;
  ELSIF result IS NOT NULL AND result<>'null'::jsonb THEN RETURN false;
  END IF;
 END LOOP;
 RETURN true;
END $$;

ALTER TABLE mcp_catalog_monitors ADD CONSTRAINT mcp_catalog_oauth_renewal_chk CHECK (mcp_catalog_oauth_renewal_valid(payload));


CREATE FUNCTION mcp_oauth_retire_deleted_renewal() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE root jsonb; changed jsonb; view record; cfg jsonb; grant_data jsonb; phase jsonb; record_data jsonb; job_id uuid; client_cipher bytea; path text[]; captured_at timestamptz:=now(); count_jobs bigint;
BEGIN
 IF TG_OP='UPDATE' AND (NEW.status<>'deleted' OR OLD.status='deleted') THEN RETURN NEW; END IF;
 SELECT payload INTO root FROM mcp_catalog_monitors WHERE app_id=OLD.id AND account_id=OLD.account_id FOR UPDATE;
 IF root IS NULL THEN IF TG_OP='DELETE' THEN RETURN OLD; ELSE RETURN NEW; END IF; END IF;
 PERFORM pg_advisory_xact_lock(hashtext('mcp_oauth_retirement'),hashtext(OLD.account_id::text));
 changed:=root;
 FOR view IN SELECT key,value FROM jsonb_each(jsonb_build_object('default',root)||COALESCE(root->'roles','{}'::jsonb)) LOOP
  cfg:=view.value->'config'->'observer'->'oauth';grant_data:=view.value->'observer_status'->'oauth_renewal'->'private_result';
  IF cfg IS NULL OR cfg='null'::jsonb OR COALESCE(grant_data->>'ciphertext','')='' THEN CONTINUE; END IF;
  SELECT count(*) INTO count_jobs FROM mcp_oauth_retirements WHERE account_id=OLD.account_id AND state IN ('awaiting_issuance','pending','held','manual_required');
  IF count_jobs>=64 THEN RAISE EXCEPTION 'MCP OAuth retirement capacity exhausted' USING ERRCODE='23514'; END IF;
  job_id:=gen_random_uuid(); client_cipher:=NULL;
  SELECT ciphertext INTO client_cipher FROM app_secrets WHERE app_id=OLD.id AND account_id=OLD.account_id AND scope='default' AND key=substring(cfg->>'client_secret_ref' FROM 8) AND managed_postgres_binding_id IS NULL AND managed_object_storage_credential_id IS NULL AND managed_credential_ref IS NULL;
  phase:=jsonb_build_object('id',job_id,'state',CASE WHEN COALESCE(cfg->>'revocation_endpoint','')='' THEN 'manual_required' ELSE 'pending' END,'attempts',0,'next_attempt_at',captured_at,'refresh_acknowledged',false,'access_acknowledged',false,'private_config',cfg,'private_ciphertext',grant_data->>'ciphertext');
  IF client_cipher IS NOT NULL THEN phase:=phase||jsonb_build_object('private_client_ciphertext',encode(client_cipher,'base64')); END IF;
  record_data:=jsonb_build_object('id',job_id,'app_id',OLD.id,'app_slug',OLD.slug,'role',view.key,'reason','app_deleted','state',phase->>'state','created_at',captured_at,'retain_until',captured_at+interval '7 days','revocation',phase,'allow_active_grant_revocation',false,'history',jsonb_build_array(jsonb_build_object('at',captured_at,'action','queued')));
  INSERT INTO mcp_oauth_retirements(id,account_id,app_id,binding_sha256,created_at,retain_until,next_attempt_at,payload)
  VALUES(job_id,OLD.account_id,OLD.id,encode(sha256(convert_to((cfg->>'token_endpoint')||chr(10)||(cfg->>'client_id'),'UTF8')),'hex'),captured_at,captured_at+interval '7 days',captured_at,record_data);
  view.value:=view.value-'roles';
  view.value:=jsonb_set(view.value,ARRAY['observer_status'],(view.value->'observer_status')-'oauth_renewal');
  IF view.key='default' THEN changed:=view.value||jsonb_build_object('roles',COALESCE(changed->'roles','{}'::jsonb));
  ELSE changed:=jsonb_set(changed,ARRAY['roles',view.key],view.value); END IF;
 END LOOP;
 UPDATE mcp_catalog_monitors SET payload=changed WHERE app_id=OLD.id AND account_id=OLD.account_id;
 IF TG_OP='DELETE' THEN RETURN OLD; ELSE RETURN NEW; END IF;
END $$;
CREATE TRIGGER mcp_oauth_aa_renewal_retirement BEFORE UPDATE OF status OR DELETE ON apps FOR EACH ROW EXECUTE FUNCTION mcp_oauth_retire_deleted_renewal();
END $mcp_replay_guard$;
-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM mcp_catalog_monitors m CROSS JOIN LATERAL jsonb_each(jsonb_build_object('default',m.payload)||COALESCE(m.payload->'roles','{}'::jsonb)) v WHERE v.value->'observer_status'->'oauth_renewal'->>'state' IN ('pending','saved','reauthorization_required')) THEN
  RAISE EXCEPTION 'resolve pending MCP OAuth renewals before rollback';
 END IF;
END $$;
-- +goose StatementEnd
DROP TRIGGER mcp_oauth_aa_renewal_retirement ON apps;
DROP FUNCTION mcp_oauth_retire_deleted_renewal();
ALTER TABLE mcp_catalog_monitors DROP CONSTRAINT mcp_catalog_oauth_renewal_chk;
DROP FUNCTION mcp_catalog_oauth_renewal_valid(jsonb);
