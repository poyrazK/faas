-- +goose Up
-- The final schema object marks this transactional Up as applied. A ledger
-- replay must preserve definitions widened by later migrations and must not
-- re-run grant backfills or replace saved rollback constraint definitions.
-- +goose StatementBegin
DO $mcp_replay_guard$ BEGIN
 IF EXISTS(SELECT 1 FROM pg_trigger WHERE tgrelid='apps'::regclass AND tgname='mcp_oauth_app_retirement') THEN RETURN; END IF;
CREATE TABLE mcp_oauth_retirements (
 id uuid PRIMARY KEY,
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 app_id uuid NOT NULL,
 binding_sha256 text NOT NULL,
 created_at timestamptz NOT NULL,
 retain_until timestamptz NOT NULL,
 next_attempt_at timestamptz NOT NULL,
 state text GENERATED ALWAYS AS (payload->>'state') STORED,
 payload jsonb NOT NULL,
 CONSTRAINT mcp_oauth_retirement_state_chk CHECK (state IN ('awaiting_issuance','pending','held','installed','manual_required','provider_acknowledged','manually_acknowledged','expired')),
 CONSTRAINT mcp_oauth_retirement_payload_chk CHECK (
  jsonb_typeof(payload->'history')='array' AND jsonb_array_length(payload->'history')<=32
  AND payload->>'id'=id::text AND payload->>'app_id'=app_id::text
  AND jsonb_typeof(payload->'revocation')='object'
  AND (payload->'revocation'->>'attempts')::int BETWEEN 0 AND 8
  AND retain_until>created_at AND retain_until<=created_at+interval '7 days'
  AND (state NOT IN ('installed','provider_acknowledged','manually_acknowledged','expired') OR NOT (payload->'revocation' ?| ARRAY['private_config','private_ciphertext','private_client_ciphertext','private_lease_id','private_lease_until']))
 )
);
CREATE INDEX mcp_oauth_retirements_due ON mcp_oauth_retirements(state,next_attempt_at);
CREATE INDEX mcp_oauth_retirements_account_history ON mcp_oauth_retirements(account_id,created_at DESC,id DESC);
CREATE INDEX mcp_oauth_retirements_binding ON mcp_oauth_retirements(account_id,binding_sha256,state);

-- Legacy monitor snapshots now carry public progress only. The canonical sealed
-- grant belongs to the account-owned retirement table, without an app FK.
ALTER TABLE mcp_catalog_monitors DROP CONSTRAINT mcp_catalog_oauth_lifecycle_chk;

CREATE FUNCTION mcp_catalog_oauth_public_lifecycle_valid(payload jsonb) RETURNS boolean
LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE view record; phase jsonb; checked jsonb:=payload; path text[];
BEGIN
 FOR view IN SELECT key,value FROM jsonb_each(jsonb_build_object('default',payload)||COALESCE(payload->'roles','{}'::jsonb)) LOOP
  phase:=view.value->'oauth_lifecycle'->'revocation';
  IF phase IS NULL OR phase='null'::jsonb THEN CONTINUE; END IF;
  IF phase ?| ARRAY['private_config','private_ciphertext','private_client_ciphertext','private_lease_id','private_lease_until'] THEN RETURN false; END IF;
  IF phase->>'state'='pending' THEN
   path:=CASE WHEN view.key='default' THEN ARRAY['oauth_lifecycle','revocation'] ELSE ARRAY['roles',view.key,'oauth_lifecycle','revocation'] END;
   checked:=jsonb_set(checked,path,phase||jsonb_build_object('private_config','{}'::jsonb,'private_ciphertext','validation_only'));
  END IF;
 END LOOP;
 RETURN mcp_catalog_oauth_lifecycle_valid(checked);
END $$;


INSERT INTO mcp_oauth_retirements(id,account_id,app_id,binding_sha256,created_at,retain_until,next_attempt_at,payload)
SELECT (phase->>'id')::uuid,m.account_id,m.app_id,
 encode(sha256(convert_to(COALESCE(phase->'private_config'->>'token_endpoint','')||chr(10)||COALESCE(phase->'private_config'->>'client_id',''),'UTF8')),'hex'),
 now(),now()+interval '7 days',COALESCE((phase->>'next_attempt_at')::timestamptz,now()),
 jsonb_build_object('id',phase->>'id','app_id',m.app_id,'app_slug',a.slug,'role',views.key,'reason','disconnect','state',phase->>'state','created_at',now(),'retain_until',now()+interval '7 days','revocation',phase,'allow_active_grant_revocation',false,'history',jsonb_build_array(jsonb_build_object('at',now(),'action','migrated')))
FROM mcp_catalog_monitors m JOIN apps a ON a.id=m.app_id
CROSS JOIN LATERAL jsonb_each(jsonb_build_object('default',m.payload)||COALESCE(m.payload->'roles','{}'::jsonb)) views
CROSS JOIN LATERAL (SELECT views.value->'oauth_lifecycle'->'revocation' AS phase) phases
WHERE phase->>'id' IS NOT NULL ON CONFLICT(id) DO NOTHING;

CREATE FUNCTION mcp_oauth_public_phase(phase jsonb) RETURNS jsonb LANGUAGE sql IMMUTABLE AS $$
 SELECT phase-ARRAY['private_config','private_ciphertext','private_client_ciphertext','private_lease_id','private_lease_until'];
$$;
DO $$ DECLARE row record; v_payload jsonb; role record; path text[];
BEGIN
 FOR row IN SELECT app_id,payload FROM mcp_catalog_monitors LOOP
  v_payload:=row.payload;
  FOR role IN SELECT key,value FROM jsonb_each(jsonb_build_object('default',v_payload)||COALESCE(v_payload->'roles','{}'::jsonb)) LOOP
   IF role.value->'oauth_lifecycle'->'revocation' IS NULL THEN CONTINUE; END IF;
   path:=CASE WHEN role.key='default' THEN ARRAY['oauth_lifecycle','revocation'] ELSE ARRAY['roles',role.key,'oauth_lifecycle','revocation'] END;
   v_payload:=jsonb_set(v_payload,path,mcp_oauth_public_phase(role.value->'oauth_lifecycle'->'revocation'));
  END LOOP;
  UPDATE mcp_catalog_monitors SET payload=v_payload WHERE app_id=row.app_id;
 END LOOP;
END $$;

ALTER TABLE mcp_catalog_monitors ADD CONSTRAINT mcp_catalog_oauth_lifecycle_chk CHECK (mcp_catalog_oauth_public_lifecycle_valid(payload));


CREATE FUNCTION mcp_oauth_retire_deleted_app() RETURNS trigger LANGUAGE plpgsql AS $$
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
  SELECT ciphertext INTO client_cipher FROM app_secrets WHERE app_id=OLD.id AND account_id=OLD.account_id AND scope='default' AND key=substring(cfg->>'client_secret_ref' FROM 8) AND managed_postgres_binding_id IS NULL AND managed_object_storage_credential_id IS NULL AND managed_credential_ref IS NULL;
  phase:=jsonb_build_object('id',job_id,'state',CASE WHEN COALESCE(cfg->>'revocation_endpoint','')='' THEN 'manual_required' ELSE 'pending' END,'attempts',0,'next_attempt_at',captured_at,'refresh_acknowledged',false,'access_acknowledged',false,'private_config',cfg,'private_ciphertext',grant_data->>'ciphertext');
  IF client_cipher IS NOT NULL THEN phase:=phase||jsonb_build_object('private_client_ciphertext',encode(client_cipher,'base64')); END IF;
  record_data:=jsonb_build_object('id',job_id,'app_id',OLD.id,'app_slug',OLD.slug,'role',view.key,'reason','app_deleted','state',phase->>'state','created_at',captured_at,'retain_until',captured_at+interval '7 days','revocation',phase,'allow_active_grant_revocation',false,'history',jsonb_build_array(jsonb_build_object('at',captured_at,'action','queued')));
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
END $$;
CREATE TRIGGER mcp_oauth_app_retirement BEFORE UPDATE OF status OR DELETE ON apps FOR EACH ROW EXECUTE FUNCTION mcp_oauth_retire_deleted_app();
END $mcp_replay_guard$;
-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM mcp_oauth_retirements) THEN RAISE EXCEPTION 'export and remove MCP OAuth retirement records before rollback'; END IF;
END $$;
-- +goose StatementEnd
DROP TRIGGER mcp_oauth_app_retirement ON apps;
DROP FUNCTION mcp_oauth_retire_deleted_app();
ALTER TABLE mcp_catalog_monitors DROP CONSTRAINT mcp_catalog_oauth_lifecycle_chk;
ALTER TABLE mcp_catalog_monitors ADD CONSTRAINT mcp_catalog_oauth_lifecycle_chk CHECK (mcp_catalog_oauth_lifecycle_valid(payload));
DROP FUNCTION mcp_catalog_oauth_public_lifecycle_valid(jsonb);
DROP FUNCTION mcp_oauth_public_phase(jsonb);
DROP TABLE mcp_oauth_retirements;
