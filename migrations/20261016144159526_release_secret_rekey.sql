-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
CREATE TABLE IF NOT EXISTS release_secret_key_retirements (
 kid text PRIMARY KEY CHECK(kid ~ '^age1[0-9a-z]{58}$'),
 retired_at timestamptz NOT NULL DEFAULT now() CHECK(isfinite(retired_at))
);

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION release_secret_document_envelopes(payload jsonb, kind text) RETURNS SETOF jsonb
LANGUAGE sql IMMUTABLE AS $$
 SELECT value FROM jsonb_array_elements(CASE WHEN kind='capture' THEN '[]'::jsonb ELSE payload END)
 UNION ALL SELECT jsonb_path_query(payload,'$.workloads[*].live_secrets[*]') WHERE kind='capture'
 UNION ALL SELECT jsonb_path_query(payload,'$.workloads[*].secret_snapshot[*]') WHERE kind='capture'
 UNION ALL SELECT jsonb_path_query(payload,'$.workloads[*].secret_target[*]') WHERE kind='capture'
$$;
-- +goose StatementEnd

-- This projection deliberately omits delivery observations. It keeps every
-- sealed configuration field and timestamp for a compare-and-swap fence.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION release_secret_app_document(s app_secrets) RETURNS jsonb LANGUAGE sql STABLE AS $$
 SELECT jsonb_build_array(jsonb_build_object('key',(s).key,'kid',coalesce((s).kid,''),
 'ciphertext',encode((s).ciphertext,'base64'),'value_hash',coalesce((s).value_hash,''),
 'secret_class',(s).secret_class,'secret_version',(s).secret_version,'delivery_version',(s).delivery_version,
 'created_at',(s).created_at,'updated_at',(s).updated_at,
 'managed_postgres_binding_id',coalesce((s).managed_postgres_binding_id::text,''),
 'managed_postgres_access','',
 'managed_credential_ref',coalesce((s).managed_credential_ref,''),
 'managed_credential_generation',coalesce((s).managed_credential_generation,0),
 'managed_object_storage_credential_id',coalesce((s).managed_object_storage_credential_id::text,'')))
$$;
-- +goose StatementEnd

CREATE OR REPLACE VIEW release_secret_rekey_documents AS
 SELECT 'app'::text AS kind, s.account_id::text||'/'||s.app_id::text||'/'||s.scope||'/'||s.key AS id,
 release_secret_app_document(s) AS payload FROM app_secrets s
 UNION ALL SELECT 'bundle',account_id::text||'/'||request_id,values_json FROM project_release_secret_bundles
 UNION ALL SELECT 'capture',account_id::text||'/'||attempt_id,payload FROM project_release_config_intents
 UNION ALL SELECT 'deployment',deployment_id::text,values_json FROM deployment_release_secret_snapshots;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION release_secret_without_envelopes(payload jsonb, kind text) RETURNS jsonb
LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE result jsonb:=payload; workloads jsonb; w jsonb; entries jsonb; field text; idx integer:=0;
BEGIN
 IF kind<>'capture' THEN
  SELECT coalesce(jsonb_agg(value-ARRAY['kid','ciphertext'] ORDER BY ord),'[]'::jsonb) INTO result
   FROM jsonb_array_elements(payload) WITH ORDINALITY AS e(value,ord);
  RETURN result;
 END IF;
 workloads:=payload->'workloads';
 FOR w IN SELECT value FROM jsonb_array_elements(workloads) LOOP
  FOREACH field IN ARRAY ARRAY['live_secrets','secret_snapshot','secret_target'] LOOP
   IF jsonb_typeof(w->field)='array' THEN
    SELECT coalesce(jsonb_agg(value-ARRAY['kid','ciphertext'] ORDER BY ord),'[]'::jsonb) INTO entries
     FROM jsonb_array_elements(w->field) WITH ORDINALITY AS e(value,ord);
    result:=jsonb_set(result,ARRAY['workloads',idx::text,field],entries);
   END IF;
  END LOOP;
  idx:=idx+1;
 END LOOP;
 RETURN result;
END $$;
-- +goose StatementEnd

CREATE TABLE IF NOT EXISTS release_secret_key_retirement_fence (
 singleton boolean PRIMARY KEY CHECK(singleton),
 generation bigint NOT NULL DEFAULT 0 CHECK(generation>=0)
);
INSERT INTO release_secret_key_retirement_fence(singleton) VALUES(true) ON CONFLICT (singleton) DO NOTHING;

-- Retirement owns the exclusive side of this lock; writes own the shared side
-- until commit. A writer that sealed before retirement cannot publish afterward.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_release_secret_key_retirement() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE document jsonb; kind text;
BEGIN
 PERFORM pg_advisory_xact_lock_shared(hashtextextended('release-secret-key-retirement',0));
 -- A transaction with an older repeatable-read snapshot must serialize or fail.
 PERFORM generation FROM release_secret_key_retirement_fence WHERE singleton FOR SHARE;
 IF TG_TABLE_NAME='app_secrets' THEN document:=jsonb_build_array(to_jsonb(NEW)); kind:='app';
 ELSIF TG_TABLE_NAME='project_release_config_intents' THEN document:=NEW.payload; kind:='capture';
 ELSE document:=NEW.values_json; kind:='array'; END IF;
 IF EXISTS(SELECT 1 FROM release_secret_document_envelopes(document,kind) e
   WHERE EXISTS(SELECT 1 FROM release_secret_key_retirements r WHERE r.kid=coalesce(e->>'kid','') OR coalesce(e->>'kid','')='')) THEN
  RAISE EXCEPTION 'encrypted release input uses a retired or unlabelled key'
   USING ERRCODE='23514',CONSTRAINT='release_secret_key_retired';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE OR REPLACE TRIGGER secret_key_retirement BEFORE INSERT OR UPDATE ON app_secrets FOR EACH ROW EXECUTE FUNCTION guard_release_secret_key_retirement();
CREATE OR REPLACE TRIGGER secret_key_retirement BEFORE INSERT OR UPDATE ON project_release_secret_bundles FOR EACH ROW EXECUTE FUNCTION guard_release_secret_key_retirement();
CREATE OR REPLACE TRIGGER secret_key_retirement BEFORE INSERT OR UPDATE ON deployment_release_secret_snapshots FOR EACH ROW EXECUTE FUNCTION guard_release_secret_key_retirement();
CREATE OR REPLACE TRIGGER secret_key_retirement BEFORE INSERT OR UPDATE ON project_release_config_intents FOR EACH ROW EXECUTE FUNCTION guard_release_secret_key_retirement();

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION protect_release_secret_envelope_update() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF current_setting('faas.release_secret_rekey',true)='on'
  AND to_jsonb(OLD)-'values_json'=to_jsonb(NEW)-'values_json'
  AND release_secret_without_envelopes(OLD.values_json,'array')=release_secret_without_envelopes(NEW.values_json,'array') THEN RETURN NEW; END IF;
 RAISE EXCEPTION 'release secret versions are immutable' USING ERRCODE='23514',CONSTRAINT='release_secret_version_immutable';
END $$;
-- +goose StatementEnd
CREATE OR REPLACE TRIGGER immutable_release_secret_envelopes BEFORE UPDATE ON project_release_secret_bundles FOR EACH ROW EXECUTE FUNCTION protect_release_secret_envelope_update();
CREATE OR REPLACE TRIGGER immutable_release_secret_envelopes BEFORE UPDATE ON deployment_release_secret_snapshots FOR EACH ROW EXECUTE FUNCTION protect_release_secret_envelope_update();

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION protect_release_configuration_snapshot() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='UPDATE' THEN
  IF TG_TABLE_NAME='project_release_config_intents' AND current_setting('faas.release_secret_rekey',true)='on'
   AND to_jsonb(OLD)-'payload'=to_jsonb(NEW)-'payload'
   AND release_secret_without_envelopes(OLD.payload,'capture')=release_secret_without_envelopes(NEW.payload,'capture') THEN RETURN NEW; END IF;
  RAISE EXCEPTION 'release configuration snapshots are immutable' USING ERRCODE='23514';
 END IF;
 IF TG_TABLE_NAME='deployment_release_env_snapshots' THEN
  IF EXISTS(SELECT 1 FROM deployments WHERE id=OLD.deployment_id) THEN RAISE EXCEPTION 'release configuration snapshots follow deployment lifetime' USING ERRCODE='23514'; END IF;
 ELSE
  IF EXISTS(SELECT 1 FROM project_submissions WHERE account_id=OLD.account_id AND attempt_id=OLD.attempt_id) THEN RAISE EXCEPTION 'release configuration snapshots follow submission lifetime' USING ERRCODE='23514'; END IF;
 END IF;
 RETURN OLD;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION reject_pinned_release_secret_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE selected_app uuid; selected_scope text;
BEGIN
 selected_app:=coalesce(NEW.app_id,OLD.app_id); selected_scope:=coalesce(NEW.scope,OLD.scope);
 PERFORM 1 FROM apps WHERE id=selected_app AND status<>'deleted' FOR UPDATE;
 IF EXISTS(SELECT 1 FROM project_release_sets r JOIN project_release_members m ON m.release_id=r.id
  JOIN deployment_release_secret_snapshots s ON s.deployment_id=m.deployment_id JOIN apps a ON a.id=m.app_id AND a.status<>'deleted'
  WHERE r.active AND r.environment_slug=selected_scope AND m.app_id=selected_app) THEN
  IF TG_OP='UPDATE' AND current_setting('faas.release_secret_rekey',true)='on'
   AND to_jsonb(OLD)-ARRAY['kid','ciphertext']=to_jsonb(NEW)-ARRAY['kid','ciphertext'] THEN RETURN NEW; END IF;
  IF TG_OP='UPDATE' AND NEW.ciphertext=OLD.ciphertext AND NEW.kid IS NOT DISTINCT FROM OLD.kid
   AND NEW.value_hash IS NOT DISTINCT FROM OLD.value_hash AND NEW.secret_version IS NOT DISTINCT FROM OLD.secret_version
   AND NEW.delivery_version=OLD.delivery_version AND NEW.app_id=OLD.app_id AND NEW.account_id=OLD.account_id AND NEW.scope=OLD.scope AND NEW.key=OLD.key
   AND NEW.managed_credential_ref IS NOT DISTINCT FROM OLD.managed_credential_ref AND NEW.managed_credential_generation IS NOT DISTINCT FROM OLD.managed_credential_generation
   AND NEW.secret_class=OLD.secret_class AND NEW.managed_postgres_binding_id IS NOT DISTINCT FROM OLD.managed_postgres_binding_id
   AND NEW.managed_object_storage_credential_id IS NOT DISTINCT FROM OLD.managed_object_storage_credential_id THEN RETURN NEW; END IF;
  RAISE EXCEPTION 'release secrets require an atomic secret deployment' USING ERRCODE='23514',CONSTRAINT='release_secret_snapshot_owned';
 END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd

-- +goose Down
-- Never erase a durable retirement barrier on downgrade.
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM release_secret_key_retirements) THEN RAISE EXCEPTION 'cannot discard secret key retirement barriers'; END IF;
END $$;
-- +goose StatementEnd
DROP TRIGGER secret_key_retirement ON app_secrets;
DROP TRIGGER secret_key_retirement ON project_release_secret_bundles;
DROP TRIGGER secret_key_retirement ON deployment_release_secret_snapshots;
DROP TRIGGER secret_key_retirement ON project_release_config_intents;
DROP TRIGGER immutable_release_secret_envelopes ON project_release_secret_bundles;
DROP TRIGGER immutable_release_secret_envelopes ON deployment_release_secret_snapshots;
DROP FUNCTION guard_release_secret_key_retirement();
DROP FUNCTION protect_release_secret_envelope_update();
DROP VIEW release_secret_rekey_documents;
DROP FUNCTION release_secret_app_document(app_secrets);
DROP FUNCTION release_secret_document_envelopes(jsonb,text);
DROP TABLE release_secret_key_retirements;
DROP TABLE release_secret_key_retirement_fence;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION protect_release_configuration_snapshot() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='UPDATE' THEN
  RAISE EXCEPTION 'release configuration snapshots are immutable' USING ERRCODE='23514';
 END IF;
 IF TG_TABLE_NAME='deployment_release_env_snapshots' THEN
  IF EXISTS(SELECT 1 FROM deployments WHERE id=OLD.deployment_id) THEN
   RAISE EXCEPTION 'release configuration snapshots follow deployment lifetime' USING ERRCODE='23514';
  END IF;
 ELSE
  IF EXISTS(SELECT 1 FROM project_submissions WHERE account_id=OLD.account_id AND attempt_id=OLD.attempt_id) THEN
   RAISE EXCEPTION 'release configuration snapshots follow submission lifetime' USING ERRCODE='23514';
  END IF;
 END IF;
 RETURN OLD;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION reject_pinned_release_secret_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE selected_app uuid; selected_scope text;
BEGIN
 selected_app := COALESCE(NEW.app_id,OLD.app_id);
 selected_scope := COALESCE(NEW.scope,OLD.scope);
 -- Serialize with configuration staging and graph publication, before deciding
 -- whether a mutable operation is admissible.
 PERFORM 1 FROM apps WHERE id=selected_app AND status<>'deleted' FOR UPDATE;
 IF EXISTS (SELECT 1 FROM project_release_sets r JOIN project_release_members m ON m.release_id=r.id
  JOIN deployment_release_secret_snapshots s ON s.deployment_id=m.deployment_id JOIN apps a ON a.id=m.app_id AND a.status<>'deleted'
  WHERE r.active AND r.environment_slug=selected_scope AND m.app_id=selected_app) THEN
  -- Delivery observations do not alter sealed configuration.
  IF TG_OP='UPDATE' AND NEW.ciphertext=OLD.ciphertext AND NEW.kid IS NOT DISTINCT FROM OLD.kid
   AND NEW.value_hash IS NOT DISTINCT FROM OLD.value_hash AND NEW.secret_version IS NOT DISTINCT FROM OLD.secret_version
   AND NEW.delivery_version=OLD.delivery_version AND NEW.app_id=OLD.app_id AND NEW.account_id=OLD.account_id AND NEW.scope=OLD.scope AND NEW.key=OLD.key
   AND NEW.managed_credential_ref IS NOT DISTINCT FROM OLD.managed_credential_ref AND NEW.managed_credential_generation IS NOT DISTINCT FROM OLD.managed_credential_generation
   AND NEW.secret_class=OLD.secret_class AND NEW.managed_postgres_binding_id IS NOT DISTINCT FROM OLD.managed_postgres_binding_id
   AND NEW.managed_object_storage_credential_id IS NOT DISTINCT FROM OLD.managed_object_storage_credential_id THEN RETURN NEW; END IF;
  RAISE EXCEPTION 'release secrets require an atomic secret deployment' USING ERRCODE='23514',CONSTRAINT='release_secret_snapshot_owned';
 END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
DROP FUNCTION release_secret_without_envelopes(jsonb,text);
