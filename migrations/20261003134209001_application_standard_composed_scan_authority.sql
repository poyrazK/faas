-- +goose Up
-- adr: 435. Native authority consumes composed findings, not component scans.
-- Numeric scan bounds below mirror pkg/api/limits.go; no new quota is added.
-- +goose StatementBegin
CREATE FUNCTION application_standard_go_json_string(value text) RETURNS text
LANGUAGE sql IMMUTABLE STRICT AS $$
 SELECT replace(replace(replace(replace(replace(to_json(value)::text,
  '<',E'\\u003c'),'>',E'\\u003e'),'&',E'\\u0026'),U&'\2028',E'\\u2028'),U&'\2029',E'\\u2029');
$$;

CREATE FUNCTION application_standard_runtime_identity_hash(identity jsonb) RETURNS text
LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE body text:='{'; field text; a jsonb; item text; items text[]:='{}';
BEGIN
 FOREACH field IN ARRAY ARRAY['format','account_id','org_id','app_id','deployment_id','scope'] LOOP
  IF jsonb_typeof(identity->field) IS DISTINCT FROM 'string' THEN RETURN NULL; END IF;
  body:=body || to_json(field)::text || ':' || application_standard_go_json_string(identity->>field) || ',';
 END LOOP;
 FOR a IN SELECT value FROM jsonb_array_elements(identity->'artifacts')
  ORDER BY CASE WHEN value->>'kind'='base-image' THEN 0 ELSE 1 END,value->>'workload_name' COLLATE "C" LOOP
  item:='{';
  FOREACH field IN ARRAY ARRAY['kind','workload_name','producer_id','producer_hash','storage_key','digest'] LOOP
   IF jsonb_typeof(a->field) IS DISTINCT FROM 'string' THEN RETURN NULL; END IF;
   item:=item || to_json(field)::text || ':' || application_standard_go_json_string(a->>field) || ',';
  END LOOP;
  item:=item || '"bytes":' || (a->>'bytes');
  FOREACH field IN ARRAY ARRAY['base_producer_id','base_input_hash'] LOOP
   IF coalesce(a->>field,'')<>'' THEN item:=item || ',' || to_json(field)::text || ':' || application_standard_go_json_string(a->>field); END IF;
  END LOOP;
  items:=array_append(items,item || '}');
 END LOOP;
 RETURN encode(sha256(convert_to(body || '"artifacts":[' || array_to_string(items,',') || ']}','UTF8')),'hex');
EXCEPTION WHEN invalid_parameter_value THEN RETURN NULL;
END;
$$;

CREATE FUNCTION application_standard_native_producer_deadline(input jsonb, artifact jsonb, now_utc timestamptz) RETURNS timestamptz
LANGUAGE plpgsql AS $$
DECLARE f deployment_registry_rootfs%ROWTYPE; origin deployment_registry_verifications%ROWTYPE;
 approval deployment_registry_verifications%ROWTYPE; owner_inputs jsonb;
BEGIN
 SELECT p.* INTO f FROM deployment_registry_rootfs_current c JOIN deployment_registry_rootfs p ON p.id=c.artifact_id
 WHERE c.deployment_id=(input->'artifact'->>'id')::uuid AND c.workload_name=artifact->>'workload_name' FOR SHARE OF p NOWAIT;
 SELECT * INTO origin FROM deployment_registry_verifications WHERE id=f.registry_verification_id FOR SHARE NOWAIT;
 SELECT v.* INTO approval FROM deployment_registry_verifications v JOIN deployments d ON d.id=v.deployment_id AND d.app_id=v.app_id
 JOIN apps a ON a.id=v.app_id AND a.account_id=v.account_id
 WHERE v.deployment_id=f.deployment_id AND v.workload_name=f.workload_name AND a.status<>'deleted'
  AND v.account_id::text=input->>'account_id' AND v.app_id::text=input->>'app_id'
  AND v.input_snapshot->>'org_id'=coalesce(a.org_id::text,'')
  AND v.input_snapshot->>'image_reference'=CASE WHEN v.workload_name='' AND d.kind='image' THEN d.image_digest
   ELSE (SELECT CASE WHEN count(*)=1 THEN min(x->>'image') END FROM jsonb_array_elements(d.sidecars) x WHERE x->>'name'=v.workload_name) END
 ORDER BY v.verified_at DESC,v.id DESC LIMIT 1 FOR SHARE OF v NOWAIT;
 IF (f.id::text=artifact->>'producer_id' AND f.input_hash=artifact->>'producer_hash'
  AND f.input_snapshot->>'kind'=artifact->>'kind' AND f.input_snapshot->>'storage_key'=artifact->>'storage_key'
  AND f.input_snapshot->>'artifact_digest'=artifact->>'digest' AND f.input_snapshot->'artifact_bytes'=artifact->'bytes'
  AND coalesce(f.input_snapshot->>'base_producer_id','')=coalesce(artifact->>'base_producer_id','')
  AND coalesce(f.input_snapshot->>'base_input_hash','')=coalesce(artifact->>'base_input_hash','')
  AND f.published_at<=now_utc AND f.published_at>=origin.verified_at AND f.expires_at<=origin.expires_at
  AND f.input_snapshot->>'registry_input_hash'=origin.input_hash AND approval.verified_at<=now_utc AND approval.expires_at>now_utc
  AND approval.input_snapshot-'proof'=origin.input_snapshot-'proof'
  AND approval.input_snapshot->'proof'->>'SubjectDigest'=origin.input_snapshot->'proof'->>'SubjectDigest') IS NOT TRUE THEN
  RAISE EXCEPTION 'native runtime producer changed' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 owner_inputs:=lock_deployment_artifact_scan_with_verification(f.id,approval.id);
 IF (approval.account_id::text=input->>'account_id' AND approval.app_id::text=input->>'app_id'
  AND approval.input_snapshot->>'org_id'=input->>'org_id' AND f.input_snapshot->>'scope'=input->'artifact'->>'scope'
  AND approval.input_snapshot->>'image_reference'=owner_inputs->>'image_reference'
  AND encode(sha256(decode(owner_inputs->>'key_der','base64')),'hex')=approval.input_snapshot->'proof'->>'PublisherKeySHA256'
  AND 'sha256:' || encode(sha256(approval.payload),'hex')=approval.input_snapshot->'proof'->>'PayloadDigest'
  AND 'sha256:' || encode(sha256(approval.signature),'hex')=approval.input_snapshot->'proof'->>'SignatureDigest') IS NOT TRUE THEN
  RAISE EXCEPTION 'native runtime publisher changed' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 RETURN approval.expires_at;
END;
$$;

CREATE FUNCTION application_standard_native_base_producer_current(artifact jsonb, now_utc timestamptz) RETURNS void
LANGUAGE plpgsql AS $$
DECLARE p base_image_producers%ROWTYPE;
BEGIN
 IF NOT pg_try_advisory_xact_lock(hashtextextended('gregale.base-producer.' || (artifact->>'storage_key'),0)) THEN
  RAISE EXCEPTION 'native runtime base is busy' USING ERRCODE='55P03',CONSTRAINT='application_standard_runtime_busy';
 END IF;
 SELECT b.* INTO p FROM base_image_producer_current c JOIN base_image_producers b ON b.id=c.producer_id
 WHERE c.storage_key=artifact->>'storage_key' FOR SHARE OF b NOWAIT;
 IF (p.id::text=artifact->>'producer_id' AND p.input_hash=artifact->>'producer_hash' AND p.published_at<=now_utc
  AND p.input_snapshot->'artifact'=jsonb_build_object('storage_key',artifact->>'storage_key','digest',artifact->>'digest','bytes',artifact->'bytes')) IS NOT TRUE THEN
  RAISE EXCEPTION 'native runtime base changed' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
END;
$$;

CREATE FUNCTION application_standard_native_scan_tree_valid(t jsonb) RETURNS boolean
LANGUAGE sql IMMUTABLE AS $$
 SELECT coalesce(jsonb_typeof(t)='object' AND jsonb_typeof(t->'Version')='number' AND t->>'Version'='1'
  AND t-ARRAY['Version','Digest','ProjectionDigest','Entries','Bytes']='{}'::jsonb
  AND jsonb_typeof(t->'Digest')='string' AND t->>'Digest' ~ '^[a-f0-9]{64}$'
  AND jsonb_typeof(t->'ProjectionDigest')='string' AND t->>'ProjectionDigest' ~ '^[a-f0-9]{64}$'
  AND jsonb_typeof(t->'Entries')='number' AND t->>'Entries' ~ '^[1-9][0-9]{0,6}$'
  AND (t->>'Entries')::bigint BETWEEN 1 AND 1000000
  AND jsonb_typeof(t->'Bytes')='number' AND t->>'Bytes' ~ '^[0-9]{1,11}$'
  AND (t->>'Bytes')::bigint BETWEEN 0 AND 34359738368,false);
$$;

CREATE FUNCTION application_standard_native_composed_report_deadline(report jsonb, view jsonb, source_hash text, now_utc timestamptz, enforce boolean) RETURNS timestamptz
LANGUAGE plpgsql AS $$
DECLARE built timestamptz; counts jsonb;
BEGIN
 built:=(report->>'scanner_db_built_at')::timestamptz;
 IF (report->>'image_digest'='sha256:' || (view->'source_tree'->>'Digest') AND report->>'artifact_digest'='sha256:' || source_hash
  AND report->>'scanner_db_status'='valid' AND octet_length(report->>'scanner_version') BETWEEN 1 AND 256
  AND octet_length(report->>'scanner_db_version') BETWEEN 1 AND 256 AND coalesce(report->>'status','')=''
  AND coalesce(report->>'scanned_at','')='' AND coalesce(report->>'error','')=''
  AND built<=now_utc AND built+interval '30 days'>now_utc AND jsonb_typeof(report->'vulnerabilities')='array'
  AND jsonb_array_length(report->'vulnerabilities')<=100000) IS NOT TRUE THEN
  RAISE EXCEPTION 'native composed report is stale' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 IF EXISTS(SELECT 1 FROM jsonb_array_elements(report->'vulnerabilities') v
  WHERE v->>'severity' IS NULL OR v->>'severity' NOT IN ('CRITICAL','HIGH','MEDIUM','LOW','UNKNOWN')) THEN
  RAISE EXCEPTION 'native composed findings are invalid' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 SELECT jsonb_build_object('critical',count(*) FILTER(WHERE v->>'severity'='CRITICAL'),'high',count(*) FILTER(WHERE v->>'severity'='HIGH'),
  'medium',count(*) FILTER(WHERE v->>'severity'='MEDIUM'),'low',count(*) FILTER(WHERE v->>'severity'='LOW'),'unknown',count(*) FILTER(WHERE v->>'severity'='UNKNOWN'))
 INTO counts FROM jsonb_array_elements(report->'vulnerabilities') v;
 IF report->'severity_counts' IS DISTINCT FROM counts OR enforce AND ((counts->>'critical')::integer>0 OR (counts->>'high')::integer>0 OR (counts->>'unknown')::integer>0) THEN
  RAISE EXCEPTION 'native composed findings block enforcement' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 RETURN built+interval '30 days';
END;
$$;

CREATE FUNCTION application_standard_native_composed_views_deadline(snapshot jsonb, identity jsonb, now_utc timestamptz, enforce boolean) RETURNS timestamptz
LANGUAGE plpgsql AS $$
DECLARE facts jsonb:=snapshot->'facts'; view jsonb; report jsonb; name text; names text[]:='{}';
 n integer; bytes bigint:=0; entries bigint:=0; deadline timestamptz;
BEGIN
 SELECT count(*) INTO n FROM jsonb_array_elements(identity->'artifacts') a WHERE a->>'kind'<>'base-image';
 IF (jsonb_typeof(facts->'views')='array' AND jsonb_typeof(snapshot->'reports')='array'
  AND jsonb_array_length(facts->'views')=n AND jsonb_array_length(snapshot->'reports')=n) IS NOT TRUE THEN
  RAISE EXCEPTION 'native composed workload membership changed' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 FOR view IN SELECT value FROM jsonb_array_elements(facts->'views') LOOP
  name:=view->>'workload_name';
  IF name IS NULL OR name=ANY(names) OR NOT EXISTS(SELECT 1 FROM jsonb_array_elements(identity->'artifacts') a
   WHERE a->>'kind'<>'base-image' AND a->>'workload_name'=name)
   OR NOT application_standard_native_scan_tree_valid(view->'source_tree') OR NOT application_standard_native_scan_tree_valid(view->'projection_tree')
   OR view->'source_tree'->'ProjectionDigest' IS DISTINCT FROM view->'projection_tree'->'ProjectionDigest'
   OR view->'source_tree'->'Entries' IS DISTINCT FROM view->'projection_tree'->'Entries'
   OR view->'source_tree'->'Bytes' IS DISTINCT FROM view->'projection_tree'->'Bytes' THEN
   RAISE EXCEPTION 'native composed view is invalid' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
  END IF;
  names:=array_append(names,name); bytes:=bytes+(view->'source_tree'->>'Bytes')::bigint; entries:=entries+(view->'source_tree'->>'Entries')::bigint;
  IF bytes>34359738368 OR entries>1000000 OR (SELECT count(*) FROM jsonb_array_elements(snapshot->'reports') r WHERE r->>'workload_name'=name)<>1 THEN
   RAISE EXCEPTION 'native composed scan exceeds bounds' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
  END IF;
  SELECT r->'report' INTO report FROM jsonb_array_elements(snapshot->'reports') r WHERE r->>'workload_name'=name;
  deadline:=least(deadline,application_standard_native_composed_report_deadline(report,view,facts->>'sources_hash',now_utc,enforce));
 END LOOP;
 RETURN deadline;
END;
$$;

CREATE OR REPLACE FUNCTION application_standard_native_artifact_deadline(input jsonb, now_utc timestamptz) RETURNS timestamptz
LANGUAGE plpgsql AS $$
DECLARE identity jsonb:=input->'runtime_artifacts'; artifact jsonb; s deployment_runtime_scans%ROWTYPE;
 deadline timestamptz; enforce boolean:=input->'settings'->>'security_policy'='enforce'; source_hash text;
BEGIN
 IF NOT input ? 'runtime_artifacts' THEN
  IF coalesce((input->'settings'->>'require_signed')::boolean,false) OR enforce THEN
   RAISE EXCEPTION 'native signed producer evidence missing' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
  END IF;
  RETURN NULL;
 END IF;
 IF (identity->>'format'='gregale.runtime-artifact-input.v1' AND identity->>'account_id'=input->>'account_id'
  AND identity->>'org_id'=input->>'org_id' AND identity->>'app_id'=input->>'app_id'
  AND identity->>'deployment_id'=input->'artifact'->>'id' AND identity->>'scope'=input->'artifact'->>'scope') IS NOT TRUE THEN
  RAISE EXCEPTION 'native runtime identity invalid' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 source_hash:=application_standard_native_source_hash(identity->'artifacts');
 IF (SELECT sum((a->>'bytes')::bigint) FROM jsonb_array_elements(identity->'artifacts') a)>34359738368 THEN
  RAISE EXCEPTION 'native runtime sources exceed bounds' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 SELECT scan.* INTO s FROM deployment_runtime_scan_current c JOIN deployment_runtime_scans scan ON scan.id=c.scan_id
 WHERE c.deployment_id=(identity->>'deployment_id')::uuid FOR SHARE OF scan NOWAIT;
 IF (s.input_snapshot-ARRAY['facts','status','reports','failure']=identity AND s.input_snapshot->>'status'='complete'
  AND jsonb_typeof(s.input_snapshot->'facts'->'version')='number' AND s.input_snapshot->'facts'->>'version'='1'
  AND s.input_snapshot->'facts'-ARRAY['version','input_hash','sources_hash','views']='{}'::jsonb
  AND s.input_snapshot->'facts'->>'input_hash'=application_standard_runtime_identity_hash(identity)
  AND s.input_snapshot->'facts'->>'sources_hash'=source_hash AND s.scanned_at<=now_utc AND s.expires_at>now_utc) IS NOT TRUE THEN
  RAISE EXCEPTION 'native composed scan missing or stale' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 deadline:=least(s.expires_at,application_standard_native_composed_views_deadline(s.input_snapshot,identity,now_utc,enforce));
 FOR artifact IN SELECT value FROM jsonb_array_elements(identity->'artifacts') LOOP
  IF artifact->>'kind'='base-image' THEN PERFORM application_standard_native_base_producer_current(artifact,now_utc);
  ELSE deadline:=least(deadline,application_standard_native_producer_deadline(input,artifact,now_utc)); END IF;
 END LOOP;
 IF deadline<=clock_timestamp() THEN
  RAISE EXCEPTION 'native composed authority expired during read' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 RETURN deadline;
EXCEPTION WHEN lock_not_available THEN
 RAISE EXCEPTION 'native composed inputs busy' USING ERRCODE='55P03',CONSTRAINT='application_standard_runtime_busy';
 WHEN invalid_text_representation OR numeric_value_out_of_range OR invalid_parameter_value OR datetime_field_overflow THEN
 RAISE EXCEPTION 'native composed inputs invalid' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
END;
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION application_standard_native_artifact_deadline(input jsonb, now_utc timestamptz) RETURNS timestamptz
LANGUAGE plpgsql AS $$
DECLARE identity jsonb; artifact jsonb; deadline timestamptz; component_deadline timestamptz; enforce boolean;
BEGIN
 enforce:=input->'settings'->>'security_policy'='enforce'; identity:=input->'runtime_artifacts';
 IF NOT input ? 'runtime_artifacts' THEN
  IF coalesce((input->'settings'->>'require_signed')::boolean,false) OR enforce THEN
   RAISE EXCEPTION 'native signed artifact evidence missing' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
  END IF;
  RETURN NULL;
 END IF;
 IF (identity->>'format'='gregale.runtime-artifact-input.v1' AND identity->>'account_id'=input->>'account_id'
  AND identity->>'org_id'=input->>'org_id' AND identity->>'app_id'=input->>'app_id'
  AND identity->>'deployment_id'=input->'artifact'->>'id' AND identity->>'scope'=input->'artifact'->>'scope'
  AND jsonb_typeof(identity->'artifacts')='array' AND jsonb_array_length(identity->'artifacts') BETWEEN 1 AND 7) IS NOT TRUE THEN
  RAISE EXCEPTION 'native artifact identity invalid' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 FOR artifact IN SELECT value FROM jsonb_array_elements(identity->'artifacts') LOOP
  IF artifact->>'kind'='base-image' THEN component_deadline:=application_standard_native_base_deadline(artifact,now_utc,enforce);
  ELSE component_deadline:=application_standard_native_component_deadline(input,artifact,now_utc,enforce); END IF;
  deadline:=least(deadline,component_deadline);
 END LOOP;
 RETURN deadline;
EXCEPTION WHEN lock_not_available THEN
 RAISE EXCEPTION 'native artifact inputs are busy' USING ERRCODE='55P03',CONSTRAINT='application_standard_runtime_busy';
END;
$$;
DROP FUNCTION application_standard_native_composed_views_deadline(jsonb,jsonb,timestamptz,boolean);
DROP FUNCTION application_standard_native_composed_report_deadline(jsonb,jsonb,text,timestamptz,boolean);
DROP FUNCTION application_standard_native_scan_tree_valid(jsonb);
DROP FUNCTION application_standard_native_base_producer_current(jsonb,timestamptz);
DROP FUNCTION application_standard_native_producer_deadline(jsonb,jsonb,timestamptz);
DROP FUNCTION application_standard_runtime_identity_hash(jsonb);
DROP FUNCTION application_standard_go_json_string(text);
-- +goose StatementEnd
