-- filename: 20261003160458628_application_standard_runtime_default_base.sql
-- adr: 435. Full-rootfs retains its separate current runtime-default drive.

-- +goose Up
-- +goose StatementBegin
CREATE FUNCTION application_standard_runtime_default_base(root_input jsonb, app_runtime text) RETURNS void
LANGUAGE plpgsql AS $$
DECLARE base jsonb; producer base_image_producers%ROWTYPE; expected_key text;
BEGIN
 IF root_input->>'kind' IS DISTINCT FROM 'full-rootfs' THEN RETURN; END IF;
 -- OCI producer intake and native consumers support linux/amd64 only.
 expected_key:=CASE WHEN coalesce(app_runtime,'')='' THEN 'base/base-amd64.ext4'
  ELSE 'base/runner-' || app_runtime || '-amd64.ext4' END;
 base:=application_standard_runtime_base_producer(root_input);
 IF (base IS NOT NULL AND base->>'storage_key'=expected_key
  AND coalesce((root_input->>'layer_start')::integer,0)=0) IS NOT TRUE THEN
  RAISE EXCEPTION 'runtime-default base binding changed' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 SELECT * INTO producer FROM base_image_producers WHERE id=(base->>'producer_id')::uuid;
 IF (producer.input_snapshot->>'layout_version'='faas-base-layout-v3'
  AND producer.input_snapshot->>'guest_init_digest' ~ '^sha256:[a-f0-9]{64}$') IS NOT TRUE THEN
  RAISE EXCEPTION 'runtime-default base boot layout changed' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
END;
$$;

CREATE OR REPLACE FUNCTION application_standard_runtime_root_producer(a public.apps, d public.deployments, workload text) RETURNS jsonb
    LANGUAGE plpgsql
    AS $_$
DECLARE p deployment_registry_rootfs%ROWTYPE; r deployment_registry_verifications%ROWTYPE;
        l deployment_sidecar_layers%ROWTYPE; image_ref text; value jsonb;
BEGIN
 SELECT f.* INTO p FROM deployment_registry_rootfs_current c
 JOIN deployment_registry_rootfs f ON f.id=c.artifact_id
 WHERE c.deployment_id=d.id AND c.workload_name=workload FOR SHARE OF f NOWAIT;
 IF NOT FOUND THEN
  RAISE EXCEPTION 'runtime producer selection is incomplete' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 SELECT * INTO r FROM deployment_registry_verifications WHERE id=p.registry_verification_id FOR SHARE NOWAIT;
 image_ref:=CASE WHEN workload='' AND d.kind='image' THEN d.image_digest ELSE
  (SELECT CASE WHEN count(*)=1 THEN min(x->>'image') END FROM jsonb_array_elements(d.sidecars) x WHERE x->>'name'=workload) END;
 IF (p.deployment_id=d.id AND p.workload_name=workload AND r.deployment_id=d.id AND r.workload_name=workload
  AND r.app_id=a.id AND r.account_id=a.account_id AND p.input_snapshot->>'app_id'=a.id::text
  AND p.input_snapshot->>'account_id'=a.account_id::text AND p.input_snapshot->>'org_id'=a.org_id::text
  AND r.input_snapshot->>'org_id'=a.org_id::text AND p.input_snapshot->>'scope'=d.scope
  AND p.input_snapshot->>'registry_input_hash'=r.input_hash AND r.input_snapshot->>'image_reference'=image_ref
  AND p.input_snapshot->>'artifact_digest' ~ '^sha256:[a-f0-9]{64}$'
  AND (p.input_snapshot->>'artifact_bytes')::bigint>0) IS NOT TRUE THEN
  RAISE EXCEPTION 'runtime producer identity changed' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 IF workload='' THEN
  IF (p.input_snapshot->>'kind' IN ('app-layer','full-rootfs') AND p.input_snapshot->>'storage_key'=d.rootfs_key
   AND p.input_snapshot->>'rootfs_path'=d.rootfs_path AND (p.input_snapshot->>'content_bytes')::bigint=d.rootfs_bytes) IS NOT TRUE THEN
   RAISE EXCEPTION 'runtime main producer changed' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
  END IF;
 ELSE
  SELECT * INTO l FROM deployment_sidecar_layers WHERE deployment_id=d.id AND sidecar_name=workload FOR SHARE NOWAIT;
  IF (p.input_snapshot->>'kind'='sidecar-layer' AND p.input_snapshot->>'storage_key'=l.storage_key
   AND (p.input_snapshot->>'content_bytes')::bigint=l.bytes AND r.input_snapshot->>'selected_reference'=l.content_digest) IS NOT TRUE THEN
   RAISE EXCEPTION 'runtime sidecar producer changed' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
  END IF;
 END IF;
 value:=jsonb_build_object('kind',p.input_snapshot->>'kind','workload_name',workload,'producer_id',p.id::text,
  'producer_hash',p.input_hash,'storage_key',p.input_snapshot->>'storage_key','digest',p.input_snapshot->>'artifact_digest',
  'bytes',(p.input_snapshot->>'artifact_bytes')::bigint);
 IF coalesce(p.input_snapshot->>'base_producer_id','')<>'' THEN
  value:=value || jsonb_build_object('base_producer_id',p.input_snapshot->>'base_producer_id','base_input_hash',p.input_snapshot->>'base_input_hash');
 END IF;
 IF coalesce(p.input_snapshot->>'base_producer_id','')<>'' THEN
  PERFORM application_standard_runtime_default_base(p.input_snapshot,coalesce(a.runtime,''));
 END IF;
 RETURN value;
END;
$_$;

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
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION application_standard_runtime_root_producer(a public.apps, d public.deployments, workload text) RETURNS jsonb
    LANGUAGE plpgsql
    AS $_$
DECLARE p deployment_registry_rootfs%ROWTYPE; r deployment_registry_verifications%ROWTYPE;
        l deployment_sidecar_layers%ROWTYPE; image_ref text; value jsonb;
BEGIN
 SELECT f.* INTO p FROM deployment_registry_rootfs_current c
 JOIN deployment_registry_rootfs f ON f.id=c.artifact_id
 WHERE c.deployment_id=d.id AND c.workload_name=workload FOR SHARE OF f NOWAIT;
 IF NOT FOUND THEN
  RAISE EXCEPTION 'runtime producer selection is incomplete' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 SELECT * INTO r FROM deployment_registry_verifications WHERE id=p.registry_verification_id FOR SHARE NOWAIT;
 image_ref:=CASE WHEN workload='' AND d.kind='image' THEN d.image_digest ELSE
  (SELECT CASE WHEN count(*)=1 THEN min(x->>'image') END FROM jsonb_array_elements(d.sidecars) x WHERE x->>'name'=workload) END;
 IF (p.deployment_id=d.id AND p.workload_name=workload AND r.deployment_id=d.id AND r.workload_name=workload
  AND r.app_id=a.id AND r.account_id=a.account_id AND p.input_snapshot->>'app_id'=a.id::text
  AND p.input_snapshot->>'account_id'=a.account_id::text AND p.input_snapshot->>'org_id'=a.org_id::text
  AND r.input_snapshot->>'org_id'=a.org_id::text AND p.input_snapshot->>'scope'=d.scope
  AND p.input_snapshot->>'registry_input_hash'=r.input_hash AND r.input_snapshot->>'image_reference'=image_ref
  AND p.input_snapshot->>'artifact_digest' ~ '^sha256:[a-f0-9]{64}$'
  AND (p.input_snapshot->>'artifact_bytes')::bigint>0) IS NOT TRUE THEN
  RAISE EXCEPTION 'runtime producer identity changed' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 IF workload='' THEN
  IF (p.input_snapshot->>'kind' IN ('app-layer','full-rootfs') AND p.input_snapshot->>'storage_key'=d.rootfs_key
   AND p.input_snapshot->>'rootfs_path'=d.rootfs_path AND (p.input_snapshot->>'content_bytes')::bigint=d.rootfs_bytes) IS NOT TRUE THEN
   RAISE EXCEPTION 'runtime main producer changed' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
  END IF;
 ELSE
  SELECT * INTO l FROM deployment_sidecar_layers WHERE deployment_id=d.id AND sidecar_name=workload FOR SHARE NOWAIT;
  IF (p.input_snapshot->>'kind'='sidecar-layer' AND p.input_snapshot->>'storage_key'=l.storage_key
   AND (p.input_snapshot->>'content_bytes')::bigint=l.bytes AND r.input_snapshot->>'selected_reference'=l.content_digest) IS NOT TRUE THEN
   RAISE EXCEPTION 'runtime sidecar producer changed' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
  END IF;
 END IF;
 value:=jsonb_build_object('kind',p.input_snapshot->>'kind','workload_name',workload,'producer_id',p.id::text,
  'producer_hash',p.input_hash,'storage_key',p.input_snapshot->>'storage_key','digest',p.input_snapshot->>'artifact_digest',
  'bytes',(p.input_snapshot->>'artifact_bytes')::bigint);
 IF coalesce(p.input_snapshot->>'base_producer_id','')<>'' THEN
  value:=value || jsonb_build_object('base_producer_id',p.input_snapshot->>'base_producer_id','base_input_hash',p.input_snapshot->>'base_input_hash');
 END IF;
 RETURN value;
END;
$_$;

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
DROP FUNCTION application_standard_runtime_default_base(jsonb,text);
-- +goose StatementEnd
