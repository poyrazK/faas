ALTER TABLE api.documents ADD COLUMN version integer NOT NULL DEFAULT 1 CHECK(version>0);
CREATE FUNCTION api.advance_import_version() RETURNS trigger
 LANGUAGE plpgsql SECURITY INVOKER SET search_path=pg_catalog AS $$
BEGIN
 IF NEW.id<>OLD.id OR NEW.owner<>OLD.owner OR NEW.tenant<>OLD.tenant OR NEW.external_key<>OLD.external_key THEN
  RAISE SQLSTATE 'PT409' USING MESSAGE='import_identity_immutable';
 END IF;
 NEW.version:=OLD.version+1;
 RETURN NEW;
END;
$$;
CREATE TRIGGER advance_import_version BEFORE UPDATE ON api.documents FOR EACH ROW EXECUTE FUNCTION api.advance_import_version();
REVOKE EXECUTE ON FUNCTION api.advance_import_version() FROM PUBLIC;

CREATE OR REPLACE FUNCTION api.preview_import(items jsonb) RETURNS jsonb
 LANGUAGE plpgsql STABLE SECURITY INVOKER SET search_path=pg_catalog AS $$
DECLARE
 item jsonb; position integer:=0; errors text[]; action text; result jsonb:='[]';
 existing_body text; existing_id text; existing_version integer; subject text:=NULLIF(current_setting('request.jwt.claims',true)::jsonb->>'sub','');
 team text:=NULLIF(current_setting('request.jwt.claims',true)::jsonb->>'tenant','');
BEGIN
 IF subject IS NULL OR team IS NULL THEN RAISE SQLSTATE 'PT403' USING MESSAGE='import_identity_required'; END IF;
 IF items IS NULL OR jsonb_typeof(items)<>'array' THEN RAISE SQLSTATE 'PT400' USING MESSAGE='import_array_required'; END IF;
 IF jsonb_array_length(items)>100 THEN RAISE SQLSTATE 'PT400' USING MESSAGE='import_preview_limit_100'; END IF;
 FOR item IN SELECT value FROM jsonb_array_elements(items) LOOP
  errors:=ARRAY[]::text[];
  IF jsonb_typeof(item)<>'object' THEN errors:=array_append(errors,'row_must_be_object');
  ELSE
   IF EXISTS(SELECT 1 FROM jsonb_object_keys(item) k WHERE k NOT IN ('key','body')) THEN errors:=array_append(errors,'unknown_fields'); END IF;
   IF COALESCE(jsonb_typeof(item->'key'),'null')<>'string' OR COALESCE(length(item->>'key'),0) NOT BETWEEN 1 AND 128 THEN errors:=array_append(errors,'key_requires_1_to_128_characters'); END IF;
   IF COALESCE(jsonb_typeof(item->'body'),'null')<>'string' OR COALESCE(length(item->>'body'),0) NOT BETWEEN 1 AND 10000 THEN errors:=array_append(errors,'body_requires_1_to_10000_characters'); END IF;
   IF jsonb_typeof(item->'key')='string' AND (SELECT count(*) FROM jsonb_array_elements(items) d WHERE d.value->'key'=item->'key')>1 THEN errors:=array_append(errors,'duplicate_input_key'); END IF;
  END IF;
  action:='invalid'; existing_id:=NULL; existing_version:=NULL;
  IF cardinality(errors)=0 THEN
   SELECT d.body,d.id::text,d.version INTO existing_body,existing_id,existing_version FROM api.documents d WHERE d.owner=subject AND d.tenant=team AND d.external_key=item->>'key';
   IF NOT FOUND THEN action:='insert';
   ELSIF existing_body=item->>'body' THEN action:='unchanged';
   ELSE action:='update'; END IF;
  END IF;
  result:=result||jsonb_build_array(jsonb_build_object('index',position,'action',action,'errors',to_jsonb(errors),'expected_id',existing_id,'expected_version',existing_version));
  position:=position+1;
 END LOOP;
 RETURN jsonb_build_object('identity',jsonb_build_object('subject',subject,'tenant',team),'items',items,'rows',result,'total',position,'valid',(SELECT count(*) FROM jsonb_array_elements(result) r WHERE r.value->>'action'<>'invalid'));
END;
$$;
-- Remove the unguarded commit signature rather than leaving a bypass RPC.
DROP FUNCTION api.commit_import(jsonb);
CREATE FUNCTION api.commit_import(items jsonb,expected jsonb,review_identity jsonb) RETURNS SETOF api.documents
 LANGUAGE plpgsql SECURITY INVOKER SET search_path=pg_catalog AS $$
DECLARE preview jsonb; item jsonb; expectation jsonb; position integer;
 claims jsonb:=current_setting('request.jwt.claims',true)::jsonb;
BEGIN
 IF review_identity IS DISTINCT FROM jsonb_build_object('subject',claims->>'sub','tenant',claims->>'tenant') THEN RAISE SQLSTATE 'PT403' USING MESSAGE='import_review_identity_mismatch'; END IF;
 preview:=api.preview_import(items);
 IF preview->>'valid'<>preview->>'total' THEN RAISE SQLSTATE 'PT400' USING MESSAGE='import_validation_failed'; END IF;
 IF expected IS NULL OR jsonb_typeof(expected)<>'array' THEN RAISE SQLSTATE 'PT400' USING MESSAGE='import_expectations_required'; END IF;
 IF jsonb_array_length(expected)<>jsonb_array_length(items) THEN RAISE SQLSTATE 'PT400' USING MESSAGE='import_expectations_required'; END IF;
 -- Check every expectation before starting any write. IDs use decimal strings
 -- to preserve bigint precision in JavaScript clients.
 FOR expectation IN SELECT value FROM jsonb_array_elements(expected) LOOP
  IF jsonb_typeof(expectation)<>'object' THEN RAISE SQLSTATE 'PT400' USING MESSAGE='invalid_import_expectation'; END IF;
  IF NOT expectation ?& ARRAY['id','version'] OR (SELECT count(*) FROM jsonb_object_keys(expectation))<>2 THEN RAISE SQLSTATE 'PT400' USING MESSAGE='invalid_import_expectation'; END IF;
  IF expectation->'id'='null'::jsonb AND expectation->'version'='null'::jsonb THEN CONTINUE; END IF;
  IF jsonb_typeof(expectation->'id')<>'string' OR (expectation->>'id')!~'^[1-9][0-9]{0,18}$'
     OR jsonb_typeof(expectation->'version')<>'number' OR (expectation->>'version')!~'^[1-9][0-9]{0,9}$' THEN
   RAISE SQLSTATE 'PT400' USING MESSAGE='invalid_import_expectation';
  END IF;
 END LOOP;
 -- Stable key ordering limits deadlocks between multi-row imports.
 FOR item,position IN SELECT value,(ordinality-1)::integer FROM jsonb_array_elements(items) WITH ORDINALITY ORDER BY value->>'key' LOOP
  expectation:=expected->position;
  IF expectation->'id'='null'::jsonb THEN
   RETURN QUERY INSERT INTO api.documents(owner,tenant,external_key,body)
    VALUES(claims->>'sub',claims->>'tenant',item->>'key',item->>'body')
    ON CONFLICT ON CONSTRAINT document_import_key DO NOTHING RETURNING *;
  ELSE
   RETURN QUERY UPDATE api.documents d SET body=item->>'body'
    WHERE d.owner=claims->>'sub' AND d.tenant=claims->>'tenant' AND d.external_key=item->>'key'
      AND d.id::text=expectation->>'id' AND d.version::text=expectation->>'version' RETURNING d.*;
  END IF;
  IF NOT FOUND THEN
   RAISE SQLSTATE 'PT409' USING MESSAGE='import_review_conflict',DETAIL=jsonb_build_object('index',position,'key',item->>'key','reason','reviewed_state_changed')::text;
  END IF;
 END LOOP;
END;
$$;
REVOKE EXECUTE ON FUNCTION api.commit_import(jsonb,jsonb,jsonb) FROM PUBLIC;
COMMENT ON FUNCTION api.commit_import(jsonb,jsonb,jsonb) IS '@gregale:rpc';
