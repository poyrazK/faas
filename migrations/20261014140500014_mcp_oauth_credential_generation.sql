-- +goose Up
-- The final schema object marks this transactional Up as applied. A ledger
-- replay must preserve definitions widened by later migrations and must not
-- re-run grant backfills or replace saved rollback constraint definitions.
-- +goose StatementBegin
DO $mcp_replay_guard$ BEGIN
 IF EXISTS(SELECT 1 FROM pg_trigger WHERE tgrelid='app_secrets'::regclass AND tgname='mcp_oauth_secret_credential_change') THEN RETURN; END IF;
CREATE FUNCTION mcp_oauth_secret_source(app uuid, scope_name text, secret_key text, version bigint) RETURNS text LANGUAGE sql IMMUTABLE AS $$
 SELECT encode(sha256(convert_to(encode(sha256(convert_to(app::text||chr(10)||scope_name||chr(10)||secret_key||chr(10)||version::text,'UTF8')),'hex'),'UTF8')),'hex');
$$;
-- Upgrade matching legacy ciphertext-based sources without resetting a grant.
DO $$ DECLARE row record; v record; secret record; changed jsonb; v_value jsonb; old_source text; new_source text;
BEGIN
 FOR row IN SELECT app_id,account_id,payload FROM mcp_catalog_monitors LOOP
  changed:=row.payload;
  FOR v IN SELECT entries.key,entries.value FROM jsonb_each(jsonb_build_object('default',row.payload)||COALESCE(row.payload->'roles','{}'::jsonb)) entries LOOP
   SELECT * INTO secret FROM app_secrets WHERE app_id=row.app_id AND account_id=row.account_id AND scope='default' AND key=substring(v.value->'config'->'observer'->'oauth'->>'refresh_token_secret_ref' FROM 8);
   IF NOT FOUND THEN CONTINUE; END IF;
   old_source:=encode(sha256(convert_to(encode(sha256(secret.ciphertext),'hex'),'UTF8')),'hex');
   new_source:=mcp_oauth_secret_source(secret.app_id,secret.scope,secret.key,secret.secret_version);
   v_value:=v.value-'roles';
   IF v_value->'observer_status'->'oauth'->>'source_sha256'=old_source THEN v_value:=jsonb_set(v_value,ARRAY['observer_status','oauth','source_sha256'],to_jsonb(new_source)); END IF;
   IF v_value->'observer_status'->'oauth_renewal'->>'private_source_sha256'=old_source THEN
    v_value:=jsonb_set(v_value,ARRAY['observer_status','oauth_renewal','private_source_sha256'],to_jsonb(new_source));
    IF v_value->'observer_status'->'oauth_renewal'->'private_result'->>'source_sha256'=old_source THEN v_value:=jsonb_set(v_value,ARRAY['observer_status','oauth_renewal','private_result','source_sha256'],to_jsonb(new_source)); END IF;
   END IF;
   IF v.key='default' THEN changed:=v_value||jsonb_build_object('roles',COALESCE(changed->'roles','{}'::jsonb)); ELSE changed:=jsonb_set(changed,ARRAY['roles',v.key],v_value); END IF;
  END LOOP;
  UPDATE mcp_catalog_monitors SET payload=changed WHERE app_id=row.app_id;
 END LOOP;
END $$;

CREATE FUNCTION mcp_oauth_secret_credential_change() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE secret_row app_secrets; root jsonb; changed jsonb; view record; cfg jsonb; st jsonb; life jsonb; grant_data jsonb; phase jsonb; record_data jsonb; journal jsonb; job_id uuid; client_cipher bytea; captured_at timestamptz:=now(); count_jobs bigint; refresh_changed boolean; deleting boolean:=TG_OP='DELETE'; history jsonb;
BEGIN
 IF deleting THEN secret_row:=OLD; ELSE secret_row:=NEW; END IF;
 IF secret_row.scope<>'default' OR secret_row.managed_postgres_binding_id IS NOT NULL OR secret_row.managed_object_storage_credential_id IS NOT NULL OR secret_row.managed_credential_ref IS NOT NULL THEN IF deleting THEN RETURN OLD; ELSE RETURN NEW; END IF; END IF;
 IF TG_OP='UPDATE' AND NEW.secret_version=OLD.secret_version THEN RETURN NEW; END IF;
 IF TG_OP='INSERT' AND EXISTS(SELECT 1 FROM app_secrets WHERE app_id=NEW.app_id AND scope=NEW.scope AND key=NEW.key) THEN RETURN NEW; END IF;
 SELECT payload INTO root FROM mcp_catalog_monitors WHERE app_id=secret_row.app_id AND account_id=secret_row.account_id FOR UPDATE;
 IF root IS NULL THEN IF deleting THEN RETURN OLD; ELSE RETURN NEW; END IF; END IF;
 PERFORM pg_advisory_xact_lock(hashtext('mcp_oauth_retirement'),hashtext(secret_row.account_id::text));changed:=root;
 FOR view IN SELECT key,value FROM jsonb_each(jsonb_build_object('default',root)||COALESCE(root->'roles','{}'::jsonb)) LOOP
  cfg:=view.value->'config'->'observer'->'oauth';
  refresh_changed:=COALESCE(cfg->>'refresh_token_secret_ref','')='secret:'||secret_row.key;
  IF NOT refresh_changed AND COALESCE(cfg->>'client_secret_ref','')<>'secret:'||secret_row.key THEN CONTINUE; END IF;
  st:=COALESCE(NULLIF(view.value->'observer_status','null'::jsonb),jsonb_build_object('state','scheduled','consecutive_failures',0));
  life:=COALESCE(NULLIF(view.value->'oauth_lifecycle','null'::jsonb),jsonb_build_object('local_state','disconnected','history','[]'::jsonb));
  client_cipher:=NULL;
  IF cfg->>'client_secret_ref'='secret:'||secret_row.key AND TG_OP<>'INSERT' THEN client_cipher:=OLD.ciphertext;
  ELSE SELECT ciphertext INTO client_cipher FROM app_secrets WHERE account_id=secret_row.account_id AND app_id=secret_row.app_id AND scope='default' AND key=substring(cfg->>'client_secret_ref' FROM 8) AND managed_postgres_binding_id IS NULL AND managed_object_storage_credential_id IS NULL AND managed_credential_ref IS NULL; END IF;
  journal:=st->'oauth_renewal';
  -- Preserve unknown issuance before invalidating an in-flight exchange.
  IF journal->>'state' IN ('pending','reauthorization_required') AND COALESCE(journal->'private_result','null'::jsonb)='null'::jsonb THEN
   job_id:=(journal->>'private_id')::uuid;
   phase:=jsonb_build_object('id',job_id,'state','pending','attempts',0,'next_attempt_at',captured_at+interval '5 minutes','refresh_acknowledged',false,'access_acknowledged',false,'private_config',cfg);
   IF client_cipher IS NOT NULL THEN phase:=phase||jsonb_build_object('private_client_ciphertext',encode(client_cipher,'base64')); END IF;
   record_data:=jsonb_build_object('id',job_id,'app_id',secret_row.app_id,'app_slug',(SELECT slug FROM apps WHERE id=secret_row.app_id),'role',view.key,'reason','credential_changed','state','awaiting_issuance','created_at',captured_at,'retain_until',captured_at+interval '7 days','revocation',phase,'allow_active_grant_revocation',false,'history',jsonb_build_array(jsonb_build_object('at',captured_at,'action','queued')));
   SELECT count(*) INTO count_jobs FROM mcp_oauth_retirements WHERE account_id=secret_row.account_id AND state IN ('awaiting_issuance','pending','held','manual_required');
   IF count_jobs>=64 THEN RAISE EXCEPTION 'MCP OAuth retirement capacity exhausted' USING ERRCODE='23514'; END IF;
   INSERT INTO mcp_oauth_retirements(id,account_id,app_id,binding_sha256,created_at,retain_until,next_attempt_at,payload) VALUES(job_id,secret_row.account_id,secret_row.app_id,encode(sha256(convert_to((cfg->>'token_endpoint')||chr(10)||(cfg->>'client_id'),'UTF8')),'hex'),captured_at,captured_at+interval '7 days',captured_at+interval '5 minutes',record_data) ON CONFLICT(id) DO NOTHING;
  END IF;
  IF refresh_changed OR deleting THEN
   FOR grant_data IN SELECT value FROM jsonb_array_elements(jsonb_build_array(st->'oauth',journal->'private_result')) LOOP
    IF COALESCE(grant_data->>'ciphertext','')='' THEN CONTINUE; END IF;
    SELECT count(*) INTO count_jobs FROM mcp_oauth_retirements WHERE account_id=secret_row.account_id AND state IN ('awaiting_issuance','pending','held','manual_required');
    IF count_jobs>=64 THEN RAISE EXCEPTION 'MCP OAuth retirement capacity exhausted' USING ERRCODE='23514'; END IF;
    job_id:=gen_random_uuid();
    phase:=jsonb_build_object('id',job_id,'state',CASE WHEN COALESCE(cfg->>'revocation_endpoint','')='' THEN 'manual_required' ELSE 'pending' END,'attempts',0,'next_attempt_at',captured_at,'refresh_acknowledged',false,'access_acknowledged',false,'private_config',cfg,'private_ciphertext',grant_data->>'ciphertext');
    IF client_cipher IS NOT NULL THEN phase:=phase||jsonb_build_object('private_client_ciphertext',encode(client_cipher,'base64')); END IF;
    record_data:=jsonb_build_object('id',job_id,'app_id',secret_row.app_id,'app_slug',(SELECT slug FROM apps WHERE id=secret_row.app_id),'role',view.key,'reason','credential_changed','state',phase->>'state','created_at',captured_at,'retain_until',captured_at+interval '7 days','revocation',phase,'allow_active_grant_revocation',false,'history',jsonb_build_array(jsonb_build_object('at',captured_at,'action','queued')));
    INSERT INTO mcp_oauth_retirements(id,account_id,app_id,binding_sha256,created_at,retain_until,next_attempt_at,payload) VALUES(job_id,secret_row.account_id,secret_row.app_id,encode(sha256(convert_to((cfg->>'token_endpoint')||chr(10)||(cfg->>'client_id'),'UTF8')),'hex'),captured_at,captured_at+interval '7 days',captured_at,record_data);
    life:=jsonb_set(life,ARRAY['revocation'],mcp_oauth_public_phase(phase));
   END LOOP;
   st:=st-ARRAY['oauth','oauth_renewal'];life:=jsonb_set(life,ARRAY['local_state'],'"disconnected"');
  ELSIF journal->>'state' IN ('pending','reauthorization_required') AND COALESCE(journal->'private_result','null'::jsonb)='null'::jsonb THEN
   st:=st-'oauth_renewal';IF st->'oauth' IS NOT NULL THEN st:=jsonb_set(st,ARRAY['oauth','state'],'"reauthorization_required"'); END IF;
  END IF;
  life:=jsonb_set(life,ARRAY['credential_state'],to_jsonb(CASE WHEN deleting THEN 'reconnect_required'::text ELSE 'credential_changed'::text END));
  SELECT jsonb_agg(value ORDER BY ord) INTO history FROM jsonb_array_elements(COALESCE(life->'history','[]'::jsonb)||jsonb_build_array(jsonb_build_object('id',gen_random_uuid(),'at',captured_at,'kind','credentials_discarded'))) WITH ORDINALITY x(value,ord) WHERE ord>greatest(0,jsonb_array_length(COALESCE(life->'history','[]'::jsonb))+1-64);
  life:=jsonb_set(life,ARRAY['history'],history);
  st:=(st-ARRAY['lease_id','lease_until','oauth_flow'])||jsonb_build_object('state','scheduled','next_check_at',captured_at);
  view.value:=jsonb_set(view.value-'roles',ARRAY['config','revision'],to_jsonb(gen_random_uuid()::text));
  view.value:=view.value||jsonb_build_object('observer_status',st,'oauth_lifecycle',life);
  IF view.key='default' THEN changed:=view.value||jsonb_build_object('roles',COALESCE(changed->'roles','{}'::jsonb)); ELSE changed:=jsonb_set(changed,ARRAY['roles',view.key],view.value); END IF;
 END LOOP;
 UPDATE mcp_catalog_monitors SET payload=changed WHERE app_id=secret_row.app_id;
 IF deleting THEN RETURN OLD; ELSE RETURN NEW; END IF;
END $$;
CREATE TRIGGER mcp_oauth_secret_credential_change BEFORE INSERT OR UPDATE OR DELETE ON app_secrets FOR EACH ROW EXECUTE FUNCTION mcp_oauth_secret_credential_change();
END $mcp_replay_guard$;
-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM mcp_catalog_monitors m CROSS JOIN LATERAL jsonb_each(jsonb_build_object('default',m.payload)||COALESCE(m.payload->'roles','{}'::jsonb)) v WHERE (v.value->'oauth_lifecycle'->>'credential_state' IS NOT NULL OR (v.value->'config'->'observer'->'oauth'->>'refresh_token_secret_ref' IS NOT NULL AND (v.value->'observer_status'->'oauth' IS NOT NULL OR v.value->'observer_status'->'oauth_renewal' IS NOT NULL)))) THEN RAISE EXCEPTION 'resolve MCP OAuth credential changes before rollback'; END IF;
END $$;
-- +goose StatementEnd
DROP TRIGGER mcp_oauth_secret_credential_change ON app_secrets;
DROP FUNCTION mcp_oauth_secret_credential_change();
DROP FUNCTION mcp_oauth_secret_source(uuid,text,text,bigint);
