-- filename: 20261003231014244_application_standard_source_native_authority.sql
-- adr: 435. Distinct source captures and fresh native leases; physical acceptance remains required.
-- +goose Up
-- +goose StatementBegin
CREATE FUNCTION application_standard_source_runtime_producer(a apps, d deployments) RETURNS jsonb
LANGUAGE plpgsql AS $$
DECLARE f source_build_rootfs%ROWTYPE; origin build_export_publications%ROWTYPE;
 base jsonb; b base_image_producers%ROWTYPE; runtime text; kind text;
BEGIN
 SELECT r.* INTO f FROM source_build_rootfs_current c JOIN source_build_rootfs r ON r.id=c.artifact_id
 WHERE c.deployment_id=d.id FOR SHARE OF c,r NOWAIT;
 IF NOT FOUND THEN
  RAISE EXCEPTION 'source runtime selection missing' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 PERFORM lock_source_build_runtime_rootfs(f.input_snapshot,f.id);
 SELECT * INTO origin FROM build_export_publications WHERE id=f.publication_id FOR SHARE NOWAIT;
 runtime:=CASE WHEN a.type='function' OR coalesce(a.runtime,'')<>'' THEN coalesce(nullif(a.runtime,''),d.handler,'') ELSE '' END;
 kind:=CASE WHEN a.type='function' OR coalesce(a.runtime,'')<>'' THEN 'function-layer' ELSE 'source-app-layer' END;
 IF (origin.input_snapshot->'claims'->>'account_id'=a.account_id::text AND origin.input_snapshot->'claims'->>'app_id'=a.id::text
  AND origin.input_snapshot->'claims'->>'org_id'=coalesce(a.org_id::text,'')
  AND origin.input_snapshot->'claims'->>'deployment_id'=d.id::text
  AND origin.input_snapshot->'claims'->>'runtime'=coalesce(a.runtime,'')
  AND (coalesce(d.source_sha256,'')='' OR origin.input_snapshot->'claims'->>'source_sha256'=d.source_sha256)
  AND d.kind IN ('tarball','dockerfile','github','preview')
  AND f.published_at>=origin.verified_at AND f.expires_at<=origin.expires_at
  AND f.input_snapshot->>'kind'=kind AND f.input_snapshot->>'runtime'=runtime
  AND f.input_snapshot->>'layout_version'='faas-app-layer-layout-v1'
  AND (kind<>'function-layer' OR f.input_snapshot->>'runner_digest' ~ '^sha256:[a-f0-9]{64}$')) IS NOT TRUE THEN
  RAISE EXCEPTION 'source runtime lineage changed' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 base:=application_standard_runtime_base_producer(f.input_snapshot);
 SELECT * INTO b FROM base_image_producers WHERE id=(base->>'producer_id')::uuid FOR SHARE NOWAIT;
 IF (base IS NOT NULL AND base->>'storage_key'=CASE WHEN runtime='' THEN 'base/base-amd64.ext4' ELSE 'base/runner-'||runtime||'-amd64.ext4' END
  AND base->>'storage_key'<>f.input_snapshot->>'storage_key' AND b.input_snapshot->>'layout_version'='faas-base-layout-v3'
  AND b.input_snapshot->>'guest_init_digest'=f.input_snapshot->>'guest_init_digest') IS NOT TRUE THEN
  RAISE EXCEPTION 'source runtime base changed' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 -- This historical projection grants no fresh trust. The Go native transaction
 -- authenticates the current signature and conversion intent through freshRuntimeScanTx.
 RETURN jsonb_build_object('kind',kind,'workload_name','','producer_id',f.id::text,'producer_hash',f.input_hash,
  'storage_key',f.input_snapshot->>'storage_key','digest',f.input_snapshot->>'artifact_digest','bytes',f.input_snapshot->'artifact_bytes',
  'base_producer_id',f.input_snapshot->>'base_producer_id','base_input_hash',f.input_snapshot->>'base_input_hash');
END;
$$;

CREATE FUNCTION application_standard_native_source_producer_deadline(input jsonb, artifact jsonb, now_utc timestamptz) RETURNS timestamptz
LANGUAGE plpgsql AS $$
DECLARE f source_build_rootfs%ROWTYPE; origin build_export_publications%ROWTYPE; approval build_export_publications%ROWTYPE;
 a apps%ROWTYPE; d deployments%ROWTYPE; owner_inputs jsonb;
BEGIN
 SELECT * INTO d FROM deployments WHERE id=(input->'artifact'->>'id')::uuid FOR UPDATE NOWAIT;
 SELECT * INTO a FROM apps WHERE id=d.app_id FOR SHARE NOWAIT;
 IF (a.id::text=input->>'app_id' AND a.account_id::text=input->>'account_id'
  AND coalesce(a.org_id::text,'')=input->>'org_id' AND d.scope=input->'artifact'->>'scope'
  AND artifact=application_standard_source_runtime_producer(a,d)) IS NOT TRUE THEN
  RAISE EXCEPTION 'native source identity changed' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 SELECT r.* INTO f FROM source_build_rootfs_current c JOIN source_build_rootfs r ON r.id=c.artifact_id
 WHERE c.deployment_id=d.id FOR SHARE OF c,r NOWAIT;
 SELECT * INTO origin FROM build_export_publications WHERE id=f.publication_id FOR SHARE NOWAIT;
 SELECT * INTO approval FROM build_export_publications WHERE build_id=origin.build_id
  AND deployment_id=d.id AND app_id=a.id AND account_id=a.account_id
 ORDER BY verified_at DESC,id DESC LIMIT 1 FOR SHARE NOWAIT;
 IF (f.published_at<=now_utc AND approval.input_snapshot->'claims'=origin.input_snapshot->'claims'
  AND approval.verified_at<=now_utc AND approval.expires_at>now_utc) IS NOT TRUE THEN
  RAISE EXCEPTION 'native source approval stale' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 owner_inputs:=lock_build_export_publication(approval.input_snapshot,approval.input_snapshot->'proof'->>'publisher_name',true);
 IF (encode(sha256(decode(owner_inputs->>'key_der','base64')),'hex')=approval.input_snapshot->'proof'->>'publisher_key_sha256'
  AND 'sha256:'||encode(sha256(approval.payload),'hex')=approval.input_snapshot->'proof'->>'payload_digest'
  AND 'sha256:'||encode(sha256(approval.signature),'hex')=approval.input_snapshot->'proof'->>'signature_digest') IS NOT TRUE THEN
  RAISE EXCEPTION 'native source publisher changed' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 -- SQL fences are not ECDSA verification. Go verifies retained proof bytes in
 -- this same transaction before a native grant or publication can commit.
 RETURN approval.expires_at;
END;
$$;

CREATE OR REPLACE FUNCTION application_standard_native_source_role(a jsonb) RETURNS text
LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE k text; n text; key text;
BEGIN
 IF jsonb_typeof(a)<>'object' OR jsonb_typeof(a->'kind') IS DISTINCT FROM 'string'
  OR jsonb_typeof(a->'workload_name') IS DISTINCT FROM 'string'
  OR jsonb_typeof(a->'storage_key') IS DISTINCT FROM 'string'
  OR jsonb_typeof(a->'digest') IS DISTINCT FROM 'string'
  OR jsonb_typeof(a->'bytes') IS DISTINCT FROM 'number' THEN RETURN NULL; END IF;
 k:=a->>'kind'; n:=a->>'workload_name'; key:=a->>'storage_key';
 IF key IN ('','.') OR octet_length(key)>512 OR left(key,1)='/' OR right(key,1)='/'
  OR position('..' IN key)>0 OR position('//' IN key)>0
  OR key ~ '(^|/)[.](/|$)' OR position(chr(92) IN key)>0
  OR position(chr(13) IN key)>0 OR position(chr(10) IN key)>0
  OR a->>'digest' !~ '^sha256:[0-9a-f]{64}$'
  OR a->>'bytes' !~ '^[1-9][0-9]{0,10}$'
  OR (a->>'bytes')::bigint>17179869184 THEN RETURN NULL; END IF;
 IF k='base-image' AND n='' THEN RETURN 'base'; END IF;
 IF k IN ('app-layer','full-rootfs','source-app-layer','function-layer') AND n='' THEN RETURN 'main'; END IF;
 IF k='sidecar-layer' AND n<>'main' AND n ~ '^[a-z0-9][a-z0-9-]{0,62}$' THEN RETURN 'sidecar:' || n; END IF;
 RETURN NULL;
EXCEPTION WHEN invalid_text_representation OR numeric_value_out_of_range THEN RETURN NULL;
END;
$$;

CREATE OR REPLACE FUNCTION application_standard_runtime_producers(a apps, d deployments) RETURNS jsonb
LANGUAGE plpgsql AS $$
DECLARE workloads text[]; workload text; root_producer jsonb; base_producer jsonb; artifacts jsonb:='[]'::jsonb;
BEGIN
 IF NOT EXISTS(SELECT 1 FROM source_build_rootfs WHERE deployment_id=d.id)
  AND NOT EXISTS(SELECT 1 FROM deployment_registry_rootfs WHERE deployment_id=d.id) THEN RETURN NULL; END IF;
 IF jsonb_typeof(d.sidecars) IS DISTINCT FROM 'array' OR EXISTS(
  SELECT 1 FROM jsonb_array_elements(d.sidecars) x GROUP BY x->>'name'
  HAVING count(*)<>1 OR coalesce(x->>'name','') !~ '^[a-z0-9][a-z0-9-]{0,62}$' OR x->>'name'='main') THEN
  RAISE EXCEPTION 'runtime workload membership changed' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 workloads:=ARRAY[''] || ARRAY(SELECT x->>'name' FROM jsonb_array_elements(d.sidecars) x WHERE coalesce(x->>'image','')<>'' ORDER BY x->>'name');
 FOREACH workload IN ARRAY workloads LOOP
  IF workload='' AND EXISTS(SELECT 1 FROM source_build_rootfs WHERE deployment_id=d.id) THEN
   root_producer:=application_standard_source_runtime_producer(a,d);
  ELSE root_producer:=application_standard_runtime_root_producer(a,d,workload); END IF;
  artifacts:=artifacts || jsonb_build_array(root_producer);
  base_producer:=application_standard_runtime_base_producer(root_producer);
  IF base_producer IS NOT NULL AND NOT EXISTS(SELECT 1 FROM jsonb_array_elements(artifacts) x
   WHERE x->>'kind'='base-image' AND x->>'producer_id'=base_producer->>'producer_id') THEN
   artifacts:=artifacts || jsonb_build_array(base_producer);
  END IF;
 END LOOP;
 SELECT jsonb_agg(x ORDER BY CASE WHEN x->>'kind'='base-image' THEN 0 ELSE 1 END,x->>'workload_name') INTO artifacts
  FROM jsonb_array_elements(artifacts) x;
 RETURN jsonb_build_object('format','gregale.runtime-artifact-input.v1','account_id',a.account_id::text,'org_id',a.org_id::text,
  'app_id',a.id::text,'deployment_id',d.id::text,'scope',d.scope,'artifacts',artifacts);
END;
$$;

CREATE OR REPLACE FUNCTION application_standard_native_producer_deadline(input jsonb, artifact jsonb, now_utc timestamp with time zone) RETURNS timestamp with time zone
    LANGUAGE plpgsql
    AS $$
DECLARE f deployment_registry_rootfs%ROWTYPE; origin deployment_registry_verifications%ROWTYPE;
 approval deployment_registry_verifications%ROWTYPE; owner_inputs jsonb;
BEGIN
 IF artifact->>'kind' IN ('source-app-layer','function-layer') THEN
  RETURN application_standard_native_source_producer_deadline(input,artifact,now_utc);
 END IF;
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
 PERFORM application_standard_runtime_default_base(f.input_snapshot,
  (SELECT coalesce(a.runtime,'') FROM apps a WHERE a.id=(input->>'app_id')::uuid));
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
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION application_standard_native_producer_deadline(input jsonb, artifact jsonb, now_utc timestamp with time zone) RETURNS timestamp with time zone
    LANGUAGE plpgsql
    AS $$
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
 PERFORM application_standard_runtime_default_base(f.input_snapshot,
  (SELECT coalesce(a.runtime,'') FROM apps a WHERE a.id=(input->>'app_id')::uuid));
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

CREATE OR REPLACE FUNCTION application_standard_runtime_producers(a apps, d deployments) RETURNS jsonb
LANGUAGE plpgsql AS $$
DECLARE workloads text[]; workload text; root_producer jsonb; base_producer jsonb; artifacts jsonb:='[]'::jsonb;
BEGIN
 IF EXISTS(SELECT 1 FROM source_build_rootfs WHERE deployment_id=d.id) THEN
  RAISE EXCEPTION 'source native evidence pending' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 IF NOT EXISTS(SELECT 1 FROM deployment_registry_rootfs WHERE deployment_id=d.id) THEN RETURN NULL; END IF;
 IF jsonb_typeof(d.sidecars) IS DISTINCT FROM 'array' OR EXISTS(
  SELECT 1 FROM jsonb_array_elements(d.sidecars) x GROUP BY x->>'name'
  HAVING count(*)<>1 OR coalesce(x->>'name','') !~ '^[a-z0-9][a-z0-9-]{0,62}$' OR x->>'name'='main') THEN
  RAISE EXCEPTION 'runtime workload membership changed' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 workloads:=ARRAY[''] || ARRAY(SELECT x->>'name' FROM jsonb_array_elements(d.sidecars) x WHERE coalesce(x->>'image','')<>'' ORDER BY x->>'name');
 FOREACH workload IN ARRAY workloads LOOP
  root_producer:=application_standard_runtime_root_producer(a,d,workload);
  artifacts:=artifacts || jsonb_build_array(root_producer);
  base_producer:=application_standard_runtime_base_producer(root_producer);
  IF base_producer IS NOT NULL AND NOT EXISTS(SELECT 1 FROM jsonb_array_elements(artifacts) x
   WHERE x->>'kind'='base-image' AND x->>'producer_id'=base_producer->>'producer_id') THEN
   artifacts:=artifacts || jsonb_build_array(base_producer);
  END IF;
 END LOOP;
 SELECT jsonb_agg(x ORDER BY CASE WHEN x->>'kind'='base-image' THEN 0 ELSE 1 END,x->>'workload_name') INTO artifacts
  FROM jsonb_array_elements(artifacts) x;
 RETURN jsonb_build_object('format','gregale.runtime-artifact-input.v1','account_id',a.account_id::text,'org_id',a.org_id::text,
  'app_id',a.id::text,'deployment_id',d.id::text,'scope',d.scope,'artifacts',artifacts);
END;
$$;

CREATE OR REPLACE FUNCTION application_standard_native_source_role(a jsonb) RETURNS text
LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE k text; n text; key text;
BEGIN
 IF jsonb_typeof(a)<>'object' OR jsonb_typeof(a->'kind') IS DISTINCT FROM 'string'
  OR jsonb_typeof(a->'workload_name') IS DISTINCT FROM 'string'
  OR jsonb_typeof(a->'storage_key') IS DISTINCT FROM 'string'
  OR jsonb_typeof(a->'digest') IS DISTINCT FROM 'string'
  OR jsonb_typeof(a->'bytes') IS DISTINCT FROM 'number' THEN RETURN NULL; END IF;
 k:=a->>'kind'; n:=a->>'workload_name'; key:=a->>'storage_key';
 IF key IN ('','.') OR octet_length(key)>512 OR left(key,1)='/' OR right(key,1)='/'
  OR position('..' IN key)>0 OR position('//' IN key)>0
  OR key ~ '(^|/)[.](/|$)' OR position(chr(92) IN key)>0
  OR position(chr(13) IN key)>0 OR position(chr(10) IN key)>0
  OR a->>'digest' !~ '^sha256:[0-9a-f]{64}$'
  OR a->>'bytes' !~ '^[1-9][0-9]{0,10}$'
  OR (a->>'bytes')::bigint>17179869184 THEN RETURN NULL; END IF;
 IF k='base-image' AND n='' THEN RETURN 'base'; END IF;
 IF k IN ('app-layer','full-rootfs') AND n='' THEN RETURN 'main'; END IF;
 IF k='sidecar-layer' AND n<>'main' AND n ~ '^[a-z0-9][a-z0-9-]{0,62}$' THEN RETURN 'sidecar:' || n; END IF;
 RETURN NULL;
EXCEPTION WHEN invalid_text_representation OR numeric_value_out_of_range THEN RETURN NULL;
END;
$$;

DROP FUNCTION application_standard_native_source_producer_deadline(jsonb,jsonb,timestamptz);
DROP FUNCTION application_standard_source_runtime_producer(apps,deployments);
-- +goose StatementEnd
