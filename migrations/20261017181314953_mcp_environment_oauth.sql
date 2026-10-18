-- +goose Up
-- +goose StatementBegin
DO $migration$ DECLARE previous jsonb; guard text; BEGIN
 IF to_regprocedure('mcp_environment_oauth_guard_base()') IS NOT NULL THEN RETURN; END IF;
 previous:=jsonb_build_array(pg_get_functiondef('mcp_oauth_secret_credential_change()'::regprocedure),pg_get_functiondef('mcp_oauth_retire_deleted_app()'::regprocedure),pg_get_functiondef('mcp_oauth_retire_deleted_renewal()'::regprocedure),pg_get_functiondef('mcp_readiness_subject(deployments)'::regprocedure),pg_get_functiondef('guard_deployment_mcp_readiness()'::regprocedure));
 EXECUTE format('CREATE FUNCTION mcp_environment_oauth_guard_base() RETURNS jsonb LANGUAGE sql IMMUTABLE AS %L',format('SELECT %L::jsonb',previous::text));
EXECUTE $definition$CREATE OR REPLACE FUNCTION mcp_oauth_secret_credential_change() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE secret_row app_secrets; root jsonb; changed jsonb; view record; cfg jsonb; st jsonb; life jsonb; grant_data jsonb; phase jsonb; record_data jsonb; journal jsonb; job_id uuid; client_cipher bytea; captured_at timestamptz:=now(); count_jobs bigint; refresh_changed boolean; deleting boolean:=TG_OP='DELETE'; history jsonb;
BEGIN
 IF deleting THEN secret_row:=OLD; ELSE secret_row:=NEW; END IF;
 IF secret_row.managed_postgres_binding_id IS NOT NULL OR secret_row.managed_object_storage_credential_id IS NOT NULL OR secret_row.managed_credential_ref IS NOT NULL THEN IF deleting THEN RETURN OLD; ELSE RETURN NEW; END IF; END IF;
 IF TG_OP='UPDATE' AND NEW.secret_version=OLD.secret_version THEN RETURN NEW; END IF;
 IF TG_OP='INSERT' AND EXISTS(SELECT 1 FROM app_secrets WHERE app_id=NEW.app_id AND scope=NEW.scope AND key=NEW.key) THEN RETURN NEW; END IF;
 SELECT payload INTO root FROM mcp_catalog_monitors WHERE app_id=secret_row.app_id AND account_id=secret_row.account_id FOR UPDATE;
 IF root IS NULL THEN IF deleting THEN RETURN OLD; ELSE RETURN NEW; END IF; END IF;
 PERFORM pg_advisory_xact_lock(hashtext('mcp_oauth_retirement'),hashtext(secret_row.account_id::text));changed:=root;
 FOR view IN SELECT key,value FROM jsonb_each(jsonb_build_object('default',root)||COALESCE(root->'roles','{}'::jsonb)) LOOP
  IF COALESCE(NULLIF(view.value#>>'{config,scope}',''),'default')<>secret_row.scope THEN CONTINUE; END IF;
  cfg:=view.value->'config'->'observer'->'oauth';
  refresh_changed:=COALESCE(cfg->>'refresh_token_secret_ref','')='secret:'||secret_row.key;
  IF NOT refresh_changed AND COALESCE(cfg->>'client_secret_ref','')<>'secret:'||secret_row.key THEN CONTINUE; END IF;
  st:=COALESCE(NULLIF(view.value->'observer_status','null'::jsonb),jsonb_build_object('state','scheduled','consecutive_failures',0));
  life:=COALESCE(NULLIF(view.value->'oauth_lifecycle','null'::jsonb),jsonb_build_object('local_state','disconnected','history','[]'::jsonb));
  client_cipher:=NULL;
  IF cfg->>'client_secret_ref'='secret:'||secret_row.key AND TG_OP<>'INSERT' THEN client_cipher:=OLD.ciphertext;
  ELSE SELECT ciphertext INTO client_cipher FROM app_secrets WHERE account_id=secret_row.account_id AND app_id=secret_row.app_id AND scope=secret_row.scope AND key=substring(cfg->>'client_secret_ref' FROM 8) AND managed_postgres_binding_id IS NULL AND managed_object_storage_credential_id IS NULL AND managed_credential_ref IS NULL; END IF;
  journal:=st->'oauth_renewal';
  -- Preserve unknown issuance before invalidating an in-flight exchange.
  IF journal->>'state' IN ('pending','reauthorization_required') AND COALESCE(journal->'private_result','null'::jsonb)='null'::jsonb THEN
   job_id:=(journal->>'private_id')::uuid;
   phase:=jsonb_build_object('id',job_id,'state','pending','attempts',0,'next_attempt_at',captured_at+interval '5 minutes','refresh_acknowledged',false,'access_acknowledged',false,'private_config',cfg);
   IF client_cipher IS NOT NULL THEN phase:=phase||jsonb_build_object('private_client_ciphertext',encode(client_cipher,'base64')); END IF;
   record_data:=jsonb_build_object('id',job_id,'app_id',secret_row.app_id,'app_slug',(SELECT slug FROM apps WHERE id=secret_row.app_id),'role',view.key,'scope',COALESCE(NULLIF(view.value#>>'{config,scope}',''),'default'),'reason','credential_changed','state','awaiting_issuance','created_at',captured_at,'retain_until',captured_at+interval '7 days','revocation',phase,'allow_active_grant_revocation',false,'history',jsonb_build_array(jsonb_build_object('at',captured_at,'action','queued')));
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
    record_data:=jsonb_build_object('id',job_id,'app_id',secret_row.app_id,'app_slug',(SELECT slug FROM apps WHERE id=secret_row.app_id),'role',view.key,'scope',COALESCE(NULLIF(view.value#>>'{config,scope}',''),'default'),'reason','credential_changed','state',phase->>'state','created_at',captured_at,'retain_until',captured_at+interval '7 days','revocation',phase,'allow_active_grant_revocation',false,'history',jsonb_build_array(jsonb_build_object('at',captured_at,'action','queued')));
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
END $$;$definition$;
EXECUTE $definition$CREATE OR REPLACE FUNCTION mcp_oauth_retire_deleted_app() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE root jsonb; changed jsonb; view record; cfg jsonb; grant_data jsonb; phase jsonb; record_data jsonb; job_id uuid; client_cipher bytea; path text[]; captured_at timestamptz:=now(); count_jobs bigint;
BEGIN
 IF TG_OP='UPDATE' AND (NEW.status<>'deleted' OR OLD.status='deleted') THEN RETURN NEW; END IF;
 SELECT payload INTO root FROM mcp_catalog_monitors WHERE app_id=OLD.id AND account_id=OLD.account_id FOR UPDATE;
 IF root IS NULL THEN IF TG_OP='DELETE' THEN RETURN OLD; ELSE RETURN NEW; END IF; END IF;
 PERFORM pg_advisory_xact_lock(hashtext('mcp_oauth_retirement'),hashtext(OLD.account_id::text));
 changed:=root;
 FOR view IN SELECT key,value FROM jsonb_each(jsonb_build_object('default',root)||COALESCE(root->'roles','{}'::jsonb)) LOOP
  cfg:=view.value->'config'->'observer'->'oauth';grant_data:=view.value->'observer_status'->'oauth';
  IF cfg IS NULL OR cfg='null'::jsonb OR COALESCE(grant_data->>'ciphertext','')='' THEN CONTINUE; END IF;
  SELECT count(*) INTO count_jobs FROM mcp_oauth_retirements WHERE account_id=OLD.account_id AND state IN ('awaiting_issuance','pending','held','manual_required');
  IF count_jobs>=64 THEN RAISE EXCEPTION 'MCP OAuth retirement capacity exhausted' USING ERRCODE='23514'; END IF;
  job_id:=gen_random_uuid(); client_cipher:=NULL;
  SELECT ciphertext INTO client_cipher FROM app_secrets WHERE app_id=OLD.id AND account_id=OLD.account_id AND scope=COALESCE(NULLIF(view.value#>>'{config,scope}',''),'default') AND key=substring(cfg->>'client_secret_ref' FROM 8) AND managed_postgres_binding_id IS NULL AND managed_object_storage_credential_id IS NULL AND managed_credential_ref IS NULL;
  phase:=jsonb_build_object('id',job_id,'state',CASE WHEN COALESCE(cfg->>'revocation_endpoint','')='' THEN 'manual_required' ELSE 'pending' END,'attempts',0,'next_attempt_at',captured_at,'refresh_acknowledged',false,'access_acknowledged',false,'private_config',cfg,'private_ciphertext',grant_data->>'ciphertext');
  IF client_cipher IS NOT NULL THEN phase:=phase||jsonb_build_object('private_client_ciphertext',encode(client_cipher,'base64')); END IF;
  record_data:=jsonb_build_object('id',job_id,'app_id',OLD.id,'app_slug',OLD.slug,'role',view.key,'scope',COALESCE(NULLIF(view.value#>>'{config,scope}',''),'default'),'reason','app_deleted','state',phase->>'state','created_at',captured_at,'retain_until',captured_at+interval '7 days','revocation',phase,'allow_active_grant_revocation',false,'history',jsonb_build_array(jsonb_build_object('at',captured_at,'action','queued')));
  INSERT INTO mcp_oauth_retirements(id,account_id,app_id,binding_sha256,created_at,retain_until,next_attempt_at,payload)
  VALUES(job_id,OLD.account_id,OLD.id,encode(sha256(convert_to((cfg->>'token_endpoint')||chr(10)||(cfg->>'client_id'),'UTF8')),'hex'),captured_at,captured_at+interval '7 days',captured_at,record_data);
  view.value:=view.value-'roles';
  view.value:=jsonb_set(view.value,'{observer_status}',(view.value->'observer_status')-ARRAY['oauth','oauth_flow','lease_id','lease_until']);
  view.value:=jsonb_set(view.value,'{config,revision}',to_jsonb(gen_random_uuid()::text));
  view.value:=jsonb_set(view.value,'{oauth_lifecycle}',jsonb_build_object('local_state','disconnected','revocation',mcp_oauth_public_phase(phase),'history',COALESCE(view.value->'oauth_lifecycle'->'history','[]'::jsonb)));
  IF view.key='default' THEN changed:=view.value||jsonb_build_object('roles',COALESCE(changed->'roles','{}'::jsonb));
  ELSE changed:=jsonb_set(changed,ARRAY['roles',view.key],view.value); END IF;
 END LOOP;
 UPDATE mcp_catalog_monitors SET payload=changed WHERE app_id=OLD.id AND account_id=OLD.account_id;
 IF TG_OP='DELETE' THEN RETURN OLD; ELSE RETURN NEW; END IF;
END $$;$definition$;
EXECUTE $definition$CREATE OR REPLACE FUNCTION mcp_oauth_retire_deleted_renewal() RETURNS trigger LANGUAGE plpgsql AS $$
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
  SELECT ciphertext INTO client_cipher FROM app_secrets WHERE app_id=OLD.id AND account_id=OLD.account_id AND scope=COALESCE(NULLIF(view.value#>>'{config,scope}',''),'default') AND key=substring(cfg->>'client_secret_ref' FROM 8) AND managed_postgres_binding_id IS NULL AND managed_object_storage_credential_id IS NULL AND managed_credential_ref IS NULL;
  phase:=jsonb_build_object('id',job_id,'state',CASE WHEN COALESCE(cfg->>'revocation_endpoint','')='' THEN 'manual_required' ELSE 'pending' END,'attempts',0,'next_attempt_at',captured_at,'refresh_acknowledged',false,'access_acknowledged',false,'private_config',cfg,'private_ciphertext',grant_data->>'ciphertext');
  IF client_cipher IS NOT NULL THEN phase:=phase||jsonb_build_object('private_client_ciphertext',encode(client_cipher,'base64')); END IF;
  record_data:=jsonb_build_object('id',job_id,'app_id',OLD.id,'app_slug',OLD.slug,'role',view.key,'scope',COALESCE(NULLIF(view.value#>>'{config,scope}',''),'default'),'reason','app_deleted','state',phase->>'state','created_at',captured_at,'retain_until',captured_at+interval '7 days','revocation',phase,'allow_active_grant_revocation',false,'history',jsonb_build_array(jsonb_build_object('at',captured_at,'action','queued')));
  INSERT INTO mcp_oauth_retirements(id,account_id,app_id,binding_sha256,created_at,retain_until,next_attempt_at,payload)
  VALUES(job_id,OLD.account_id,OLD.id,encode(sha256(convert_to((cfg->>'token_endpoint')||chr(10)||(cfg->>'client_id'),'UTF8')),'hex'),captured_at,captured_at+interval '7 days',captured_at,record_data);
  view.value:=view.value-'roles';
  view.value:=jsonb_set(view.value,ARRAY['observer_status'],(view.value->'observer_status')-'oauth_renewal');
  IF view.key='default' THEN changed:=view.value||jsonb_build_object('roles',COALESCE(changed->'roles','{}'::jsonb));
  ELSE changed:=jsonb_set(changed,ARRAY['roles',view.key],view.value); END IF;
 END LOOP;
 UPDATE mcp_catalog_monitors SET payload=changed WHERE app_id=OLD.id AND account_id=OLD.account_id;
 IF TG_OP='DELETE' THEN RETURN OLD; ELSE RETURN NEW; END IF;
END $$;$definition$;
EXECUTE $definition$CREATE OR REPLACE FUNCTION mcp_readiness_subject(d deployments) RETURNS text LANGUAGE sql STABLE AS $$
 WITH bindings AS (
 SELECT jsonb_agg(jsonb_build_array(role.value->>'name',
   connection.view#>'{config,revision}', COALESCE(NULLIF(connection.view#>>'{config,scope}',''),'default'), connection.view#>'{config,enabled}',
   connection.view#>'{config,observer,enabled}',connection.view#>'{config,observer,paused}',connection.view#>'{config,observer,endpoint}',
   connection.view#>'{config,observer,oauth}' IS NOT NULL AND connection.view#>'{config,observer,oauth}'<>'null'::jsonb,
   connection.view#>'{observer_status,oauth,state}',connection.view#>'{observer_status,oauth,source_sha256}',
   connection.view#>'{observer_status,oauth,retirement_id}',connection.view#>'{observer_status,oauth,granted_scopes}',
   connection.view#>'{observer_status,oauth_flow,state_sha256}',connection.view#>'{observer_status,oauth_flow,consumed}',
   connection.view#>'{oauth_lifecycle,credential_state}',connection.view#>'{oauth_lifecycle,local_state}') ORDER BY role.value->>'name') AS value
 FROM app_mcp_readiness_policies p CROSS JOIN LATERAL jsonb_array_elements(p.policy->'roles') AS role(value)
 LEFT JOIN mcp_catalog_monitors m ON m.app_id=p.app_id AND m.account_id=(SELECT account_id FROM apps WHERE id=p.app_id)
 LEFT JOIN LATERAL (SELECT CASE WHEN role.value#>'{monitor,oauth_connections}' IS NOT NULL THEN role.value#>'{monitor,oauth_connections}'->COALESCE(NULLIF(d.scope,''),'default')
 ELSE CASE WHEN COALESCE(NULLIF(role.value#>>'{monitor,oauth_connection,scope}',''),'default')=COALESCE(NULLIF(d.scope,''),'default') THEN role.value#>'{monitor,oauth_connection}' END END AS ref) binding ON true
 LEFT JOIN LATERAL (SELECT CASE WHEN binding.ref->>'role'='default' THEN m.payload ELSE m.payload->'roles'->(binding.ref->>'role') END AS candidate) selected ON true
 LEFT JOIN LATERAL (SELECT CASE WHEN COALESCE(NULLIF(selected.candidate#>>'{config,scope}',''),'default')=COALESCE(NULLIF(d.scope,''),'default') THEN selected.candidate END AS view) connection ON true
 WHERE p.app_id=d.app_id AND ((role.value#>'{monitor,oauth_connection}' IS NOT NULL AND role.value#>'{monitor,oauth_connection}'<>'null'::jsonb) OR COALESCE(role.value#>'{monitor,oauth_connections}','{}'::jsonb)<>'{}'::jsonb))
 SELECT CASE WHEN bindings.value IS NULL THEN mcp_readiness_runtime_subject(d)
 ELSE encode(sha256(convert_to(jsonb_build_array(mcp_readiness_runtime_subject(d),bindings.value)::text,'UTF8')),'hex') END FROM bindings
$$;$definition$;
guard:=pg_get_functiondef('guard_deployment_mcp_readiness()'::regprocedure);
 IF position('subject:=mcp_readiness_subject(NEW);' IN guard)=0 THEN RAISE EXCEPTION 'unsupported readiness guard'; END IF;
 EXECUTE replace(guard,'subject:=mcp_readiness_subject(NEW);','IF EXISTS(SELECT 1 FROM jsonb_array_elements(cfg->''roles'') r WHERE COALESCE(r#>''{monitor,oauth_connections}'',''{}''::jsonb)<>''{}''::jsonb) THEN PERFORM app_id FROM mcp_catalog_monitors WHERE app_id=NEW.app_id FOR SHARE; END IF; subject:=mcp_readiness_subject(NEW);');
EXECUTE $scope_guard$CREATE OR REPLACE FUNCTION mcp_catalog_environment_scope_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE v record; previous jsonb; scope_name text; BEGIN
 FOR v IN SELECT key,value FROM jsonb_each(jsonb_build_object('default',NEW.payload)||COALESCE(NEW.payload->'roles','{}'::jsonb)) LOOP
  scope_name:=COALESCE(NULLIF(v.value#>>'{config,scope}',''),'default');
  IF scope_name !~ '^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$' THEN RAISE EXCEPTION 'invalid MCP connection environment' USING ERRCODE='23514'; END IF;
  IF TG_OP='UPDATE' THEN
   previous:=CASE WHEN v.key='default' THEN OLD.payload ELSE OLD.payload->'roles'->v.key END;
   IF COALESCE(previous#>>'{config,revision}','')<>'' AND scope_name<>COALESCE(NULLIF(previous#>>'{config,scope}',''),'default') THEN RAISE EXCEPTION 'MCP connection environment is immutable' USING ERRCODE='23514'; END IF;
  END IF;
 END LOOP;
 RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS mcp_catalog_environment_scope_guard ON mcp_catalog_monitors;
CREATE TRIGGER mcp_catalog_environment_scope_guard BEFORE INSERT OR UPDATE ON mcp_catalog_monitors FOR EACH ROW EXECUTE FUNCTION mcp_catalog_environment_scope_guard();
$scope_guard$;
END $migration$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ DECLARE definition text; BEGIN
 IF EXISTS(SELECT 1 FROM mcp_catalog_monitors m CROSS JOIN LATERAL jsonb_each(jsonb_build_object('default',m.payload)||COALESCE(m.payload->'roles','{}'::jsonb)) v WHERE COALESCE(NULLIF(v.value#>>'{config,scope}',''),'default')<>'default') OR EXISTS(SELECT 1 FROM app_mcp_readiness_policies p CROSS JOIN LATERAL jsonb_array_elements(p.policy->'roles') r WHERE COALESCE(r#>'{monitor,oauth_connections}','{}'::jsonb)<>'{}'::jsonb OR COALESCE(NULLIF(r#>>'{monitor,oauth_connection,scope}',''),'default')<>'default') THEN RAISE EXCEPTION 'remove environment OAuth bindings before rollback'; END IF;
 IF EXISTS(SELECT 1 FROM mcp_oauth_retirements WHERE COALESCE(NULLIF(payload->>'scope',''),'default')<>'default' AND state IN ('pending','held','awaiting_issuance','manual_required')) THEN RAISE EXCEPTION 'resolve environment OAuth retirements before rollback'; END IF;
 FOR definition IN SELECT jsonb_array_elements_text(mcp_environment_oauth_guard_base()) LOOP EXECUTE definition; END LOOP;
END $$;
DROP TRIGGER mcp_catalog_environment_scope_guard ON mcp_catalog_monitors;
DROP FUNCTION mcp_catalog_environment_scope_guard();
DROP FUNCTION mcp_environment_oauth_guard_base();
-- +goose StatementEnd
