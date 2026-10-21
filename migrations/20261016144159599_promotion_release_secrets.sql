-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION release_secret_document_envelopes(payload jsonb, kind text) RETURNS SETOF jsonb
LANGUAGE sql IMMUTABLE AS $$
 SELECT value FROM jsonb_array_elements(CASE WHEN kind IN ('capture','promotion') THEN '[]'::jsonb ELSE payload END)
 UNION ALL SELECT jsonb_path_query(payload,'$.workloads[*].live_secrets[*]') WHERE kind IN ('capture','promotion')
 UNION ALL SELECT jsonb_path_query(payload,'$.workloads[*].secret_snapshot[*]') WHERE kind IN ('capture','promotion')
 UNION ALL SELECT jsonb_path_query(payload,'$.workloads[*].secret_target[*]') WHERE kind IN ('capture','promotion')
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION release_secret_without_envelopes(payload jsonb, kind text) RETURNS jsonb
LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE result jsonb:=payload; workloads jsonb; w jsonb; entries jsonb; field text; idx integer:=0;
BEGIN
 IF kind NOT IN ('capture','promotion') THEN
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
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_release_secret_key_retirement() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE document jsonb; kind text;
BEGIN
 PERFORM pg_advisory_xact_lock_shared(hashtextextended('release-secret-key-retirement',0));
 -- A transaction with an older repeatable-read snapshot must serialize or fail.
 PERFORM generation FROM release_secret_key_retirement_fence WHERE singleton FOR SHARE;
 IF TG_TABLE_NAME='app_secrets' THEN document:=jsonb_build_array(to_jsonb(NEW)); kind:='app';
 ELSIF TG_TABLE_NAME IN ('project_release_config_intents','project_environment_promotion_release_config') THEN document:=NEW.payload; kind:='capture';
 ELSE document:=NEW.values_json; kind:='array'; END IF;
 IF EXISTS(SELECT 1 FROM release_secret_document_envelopes(document,kind) e
   WHERE EXISTS(SELECT 1 FROM release_secret_key_retirements r WHERE r.kid=coalesce(e->>'kid','') OR coalesce(e->>'kid','')='')) THEN
  RAISE EXCEPTION 'encrypted release input uses a retired or unlabelled key'
   USING ERRCODE='23514',CONSTRAINT='release_secret_key_retired';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION protect_promotion_release_configuration() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='UPDATE' AND current_setting('faas.release_secret_rekey',true)='on'
  AND to_jsonb(OLD)-'payload'=to_jsonb(NEW)-'payload'
  AND release_secret_without_envelopes(OLD.payload,'promotion')=release_secret_without_envelopes(NEW.payload,'promotion') THEN RETURN NEW; END IF;
 IF TG_OP='UPDATE' OR EXISTS(SELECT 1 FROM project_environment_promotions WHERE id=OLD.promotion_id) THEN
  RAISE EXCEPTION 'promotion release configuration follows promotion lifetime' USING ERRCODE='23514';
 END IF;
 RETURN OLD;
END $$;
-- +goose StatementEnd
CREATE OR REPLACE VIEW release_secret_rekey_documents AS
 SELECT 'app'::text AS kind, s.account_id::text||'/'||s.app_id::text||'/'||s.scope||'/'||s.key AS id,
 release_secret_app_document(s) AS payload FROM app_secrets s
 UNION ALL SELECT 'bundle',account_id::text||'/'||request_id,values_json FROM project_release_secret_bundles
 UNION ALL SELECT 'capture',account_id::text||'/'||attempt_id,payload FROM project_release_config_intents
 UNION ALL SELECT 'deployment',deployment_id::text,values_json FROM deployment_release_secret_snapshots
 UNION ALL SELECT 'promotion',promotion_id::text,payload FROM project_environment_promotion_release_config WHERE payload ? 'secrets';
CREATE OR REPLACE TRIGGER secret_key_retirement BEFORE INSERT OR UPDATE ON project_environment_promotion_release_config FOR EACH ROW EXECUTE FUNCTION guard_release_secret_key_retirement();

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM project_environment_promotion_release_config WHERE payload ? 'secrets') THEN
  RAISE EXCEPTION 'cannot discard promotion secret recovery and encryption maintenance';
 END IF;
END $$;
-- +goose StatementEnd
DROP TRIGGER secret_key_retirement ON project_environment_promotion_release_config;
CREATE OR REPLACE VIEW release_secret_rekey_documents AS
 SELECT 'app'::text AS kind, s.account_id::text||'/'||s.app_id::text||'/'||s.scope||'/'||s.key AS id,
 release_secret_app_document(s) AS payload FROM app_secrets s
 UNION ALL SELECT 'bundle',account_id::text||'/'||request_id,values_json FROM project_release_secret_bundles
 UNION ALL SELECT 'capture',account_id::text||'/'||attempt_id,payload FROM project_release_config_intents
 UNION ALL SELECT 'deployment',deployment_id::text,values_json FROM deployment_release_secret_snapshots;
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION release_secret_document_envelopes(payload jsonb, kind text) RETURNS SETOF jsonb
LANGUAGE sql IMMUTABLE AS $$
 SELECT value FROM jsonb_array_elements(CASE WHEN kind='capture' THEN '[]'::jsonb ELSE payload END)
 UNION ALL SELECT jsonb_path_query(payload,'$.workloads[*].live_secrets[*]') WHERE kind='capture'
 UNION ALL SELECT jsonb_path_query(payload,'$.workloads[*].secret_snapshot[*]') WHERE kind='capture'
 UNION ALL SELECT jsonb_path_query(payload,'$.workloads[*].secret_target[*]') WHERE kind='capture'
$$;
-- +goose StatementEnd
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
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION protect_promotion_release_configuration() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='UPDATE' OR EXISTS(SELECT 1 FROM project_environment_promotions WHERE id=OLD.promotion_id) THEN
  RAISE EXCEPTION 'promotion release configuration follows promotion lifetime' USING ERRCODE='23514';
 END IF;
 RETURN OLD;
END $$;
-- +goose StatementEnd
