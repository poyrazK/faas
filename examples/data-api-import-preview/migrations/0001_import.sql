CREATE SCHEMA IF NOT EXISTS api;
CREATE TABLE api.documents (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 owner text NOT NULL, tenant text NOT NULL,
 external_key text NOT NULL CHECK(length(external_key) BETWEEN 1 AND 128),
 body text NOT NULL CHECK(length(body) BETWEEN 1 AND 10000),
 CONSTRAINT document_import_key UNIQUE(owner,tenant,external_key)
);
ALTER TABLE api.documents ENABLE ROW LEVEL SECURITY;
CREATE POLICY own_documents ON api.documents
 USING(owner=current_setting('request.jwt.claims',true)::jsonb->>'sub' AND tenant=current_setting('request.jwt.claims',true)::jsonb->>'tenant')
 WITH CHECK(owner=current_setting('request.jwt.claims',true)::jsonb->>'sub' AND tenant=current_setting('request.jwt.claims',true)::jsonb->>'tenant');

CREATE FUNCTION api.preview_import(items jsonb) RETURNS jsonb
 LANGUAGE plpgsql STABLE SECURITY INVOKER SET search_path=pg_catalog AS $$
DECLARE
 item jsonb; position integer:=0; errors text[]; action text; result jsonb:='[]';
 existing_body text; subject text:=NULLIF(current_setting('request.jwt.claims',true)::jsonb->>'sub','');
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
  action:='invalid';
  IF cardinality(errors)=0 THEN
   SELECT d.body INTO existing_body FROM api.documents d WHERE d.owner=subject AND d.tenant=team AND d.external_key=item->>'key';
   IF NOT FOUND THEN action:='insert';
   ELSIF existing_body=item->>'body' THEN action:='unchanged';
   ELSE action:='update'; END IF;
  END IF;
  result:=result||jsonb_build_array(jsonb_build_object('index',position,'action',action,'errors',to_jsonb(errors)));
  position:=position+1;
 END LOOP;
 RETURN jsonb_build_object('rows',result,'total',position,'valid',(SELECT count(*) FROM jsonb_array_elements(result) r WHERE r.value->>'action'<>'invalid'));
END;
$$;
CREATE FUNCTION api.commit_import(items jsonb) RETURNS SETOF api.documents
 LANGUAGE plpgsql SECURITY INVOKER SET search_path=pg_catalog AS $$
DECLARE preview jsonb; item jsonb; claims jsonb:=current_setting('request.jwt.claims',true)::jsonb;
BEGIN
 preview:=api.preview_import(items);
 IF preview->>'valid'<>preview->>'total' THEN RAISE SQLSTATE 'PT400' USING MESSAGE='import_validation_failed'; END IF;
 FOR item IN SELECT value FROM jsonb_array_elements(items) LOOP
  RETURN QUERY INSERT INTO api.documents(owner,tenant,external_key,body)
   VALUES(claims->>'sub',claims->>'tenant',item->>'key',item->>'body')
   ON CONFLICT ON CONSTRAINT document_import_key DO UPDATE SET body=EXCLUDED.body
   RETURNING *;
 END LOOP;
END;
$$;
REVOKE EXECUTE ON FUNCTION api.preview_import(jsonb),api.commit_import(jsonb) FROM PUBLIC;
COMMENT ON FUNCTION api.preview_import(jsonb) IS '@gregale:rpc';
COMMENT ON FUNCTION api.commit_import(jsonb) IS '@gregale:rpc';
