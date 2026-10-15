-- filename: 20261001214424605_application_standard_runtime_producer_capture.sql
-- adr: 393. Capture exact private producers under native input fences.

-- +goose Up
-- +goose StatementBegin
-- This projection is immutable producer identity, not renewed approval or a
-- native consumed-byte ACK. Compatibility scan bindings remain until the
-- separate approval lease and content-aware native protocol are installed.
CREATE OR REPLACE FUNCTION application_standard_runtime_root_producer(a apps, d deployments, workload text) RETURNS jsonb
LANGUAGE plpgsql AS $$
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
$$;

CREATE OR REPLACE FUNCTION application_standard_runtime_base_producer(root_producer jsonb) RETURNS jsonb
LANGUAGE plpgsql AS $$
DECLARE p base_image_producers%ROWTYPE; selected uuid;
BEGIN
 IF NOT root_producer ? 'base_producer_id' THEN RETURN NULL; END IF;
 SELECT * INTO p FROM base_image_producers WHERE id=(root_producer->>'base_producer_id')::uuid FOR SHARE NOWAIT;
 IF NOT FOUND OR p.input_hash IS DISTINCT FROM root_producer->>'base_input_hash' THEN
  RAISE EXCEPTION 'runtime base producer changed' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 IF NOT pg_try_advisory_xact_lock(hashtextextended('gregale.base-producer.' || p.storage_key,0)) THEN
  RAISE EXCEPTION 'runtime base producer is busy' USING ERRCODE='55P03',CONSTRAINT='application_standard_runtime_busy';
 END IF;
 SELECT producer_id INTO selected FROM base_image_producer_current WHERE storage_key=p.storage_key FOR SHARE NOWAIT;
 IF selected IS DISTINCT FROM p.id OR p.storage_key IS NOT DISTINCT FROM root_producer->>'storage_key' THEN
  RAISE EXCEPTION 'runtime base producer selection changed' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 RETURN jsonb_build_object('kind','base-image','workload_name','','producer_id',p.id::text,'producer_hash',p.input_hash,
  'storage_key',p.storage_key,'digest',p.input_snapshot->'artifact'->>'digest','bytes',(p.input_snapshot->'artifact'->>'bytes')::bigint);
END;
$$;

CREATE OR REPLACE FUNCTION application_standard_runtime_producers(a apps, d deployments) RETURNS jsonb
LANGUAGE plpgsql AS $$
DECLARE workloads text[]; workload text; root_producer jsonb; base_producer jsonb; artifacts jsonb:='[]'::jsonb;
BEGIN
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

CREATE OR REPLACE FUNCTION application_standard_runtime_snapshot(application_id uuid, artifact_id uuid) RETURNS jsonb
    LANGUAGE plpgsql
    AS $$
DECLARE a apps%ROWTYPE; e app_application_standards%ROWTYPE;
        acct accounts%ROWTYPE; o orgs%ROWTYPE; d deployments%ROWTYPE; producers jsonb;
BEGIN
 SELECT * INTO a FROM apps WHERE id=application_id FOR SHARE NOWAIT;
 IF NOT FOUND THEN
  RAISE EXCEPTION 'runtime application is missing' USING ERRCODE='23514',CONSTRAINT='application_standards_pending';
 END IF;
 SELECT * INTO o FROM orgs WHERE id=a.org_id FOR SHARE NOWAIT;
 SELECT * INTO acct FROM accounts WHERE id=a.account_id FOR SHARE NOWAIT;
 SELECT * INTO e FROM app_application_standards WHERE app_id=a.id FOR SHARE NOWAIT;
 IF NOT FOUND OR a.status='deleted' OR acct.status NOT IN ('active','past_due') OR acct.abuse_hold_at IS NOT NULL OR o.status NOT IN ('active','past_due') OR o.deleted_pending
   OR e.org_id IS DISTINCT FROM a.org_id OR e.project_id IS DISTINCT FROM a.project_id
   OR NOT ((e.state='unmanaged' AND e.adoptions='[]'::jsonb AND cardinality(e.materialized_fields)=0)
     OR (e.state IN ('persisted','observed') AND e.persisted_revision=e.desired_revision AND e.effective_hash <> '')) THEN
  RAISE EXCEPTION 'runtime application standards are incomplete' USING ERRCODE='23514',CONSTRAINT='application_standards_pending';
 END IF;
 IF NOT pg_try_advisory_xact_lock(hashtextextended('gregale.application-standard.controls.' || a.id::text,0)) THEN
  RAISE EXCEPTION 'runtime controls are busy' USING ERRCODE='55P03',CONSTRAINT='application_standard_runtime_busy';
 END IF;
 IF artifact_id IS NOT NULL THEN
  SELECT * INTO d FROM deployments WHERE id=artifact_id FOR SHARE NOWAIT;
  IF NOT FOUND OR d.app_id IS DISTINCT FROM a.id THEN
   RAISE EXCEPTION 'runtime artifact belongs to another application' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_identity';
  END IF;
  IF NOT pg_try_advisory_xact_lock(hashtextextended('gregale.application-standard.artifact-children.' || d.id::text,0)) THEN
   RAISE EXCEPTION 'runtime artifact children are busy' USING ERRCODE='55P03',CONSTRAINT='application_standard_runtime_busy';
  END IF;
 END IF;
 IF artifact_id IS NOT NULL THEN producers:=application_standard_runtime_producers(a,d); END IF;
 RETURN jsonb_build_object(
  'app_id',a.id::text,'org_id',a.org_id::text,'project_id',coalesce(a.project_id::text,''),'account_id',a.account_id::text,
  'account_plan',acct.plan,'account_egress_allowlist_extra',acct.egress_allowlist_extra,
  'desired_revision',e.desired_revision,'persisted_revision',e.persisted_revision,'effective_hash',e.effective_hash,
  'adoptions',e.adoptions,'materialized_fields',to_jsonb(e.materialized_fields),'effective',e.effective,
  'base_settings',e.base_settings,'local_settings',e.local_settings,'additional_log_destinations',to_jsonb(e.additional_log_destinations::text[]),
  'settings',jsonb_build_object('require_signed',a.require_signed,'security_policy',a.security_policy,
    'egress_cidrs',to_jsonb(a.egress_allowlist::text[]),'egress_extra_ports',to_jsonb(a.egress_ports)),
  'runtime',jsonb_build_object('type',a.type,'runtime',coalesce(a.runtime,''),'ram_mb',a.ram_mb,'app_protocol',a.app_protocol),
  'drains',coalesce((SELECT jsonb_agg(jsonb_build_object('id',r.id::text,'kind',r.kind,'enabled',r.enabled,
    'target_hash',encode(sha256(convert_to(r.target_url,'UTF8')),'hex'),
    'auth_hash',encode(sha256(coalesce(r.auth_header_sealed,''::bytea)),'hex')) ORDER BY r.id)
    FROM app_log_drains r WHERE r.app_id=a.id),'[]'::jsonb),
  'signers',coalesce((SELECT jsonb_agg(jsonb_build_object('name',r.signer_name,
    'fingerprint',encode(sha256(r.cosign_public_key),'hex')) ORDER BY r.signer_name)
    FROM app_trusted_signers r WHERE r.app_id=a.id),'[]'::jsonb),
  'artifact',CASE WHEN artifact_id IS NULL THEN '{}'::jsonb ELSE jsonb_build_object(
    'id',d.id::text,'scope',d.scope,'kind',d.kind,'image_digest',coalesce(d.image_digest,''),
    'rootfs_key',coalesce(d.rootfs_key,''),'rootfs_path',coalesce(d.rootfs_path,''),'rootfs_bytes',coalesce(d.rootfs_bytes,0),
    'source_sha256',coalesce(d.source_sha256,''),'parked_reason',coalesce(d.parked_reason,''),
    'scan_status',d.scan_status,'scan_result_hash',encode(sha256(convert_to(coalesce(d.scan_result::text,''),'UTF8')),'hex'),
    'sidecars',coalesce((SELECT jsonb_agg(jsonb_build_object('sidecar_name',r.sidecar_name,
      'storage_key',r.storage_key,'bytes',r.bytes,'content_digest',r.content_digest) ORDER BY r.sidecar_name)
      FROM deployment_sidecar_layers r WHERE r.deployment_id=d.id),'[]'::jsonb)) END)
  || CASE WHEN producers IS NULL THEN '{}'::jsonb ELSE jsonb_build_object('runtime_artifacts',producers) END;
EXCEPTION WHEN lock_not_available THEN
 RAISE EXCEPTION 'runtime inputs are busy' USING ERRCODE='55P03',CONSTRAINT='application_standard_runtime_busy';
END;
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION application_standard_runtime_snapshot(application_id uuid, artifact_id uuid) RETURNS jsonb
    LANGUAGE plpgsql
    AS $$
DECLARE a apps%ROWTYPE; e app_application_standards%ROWTYPE;
        acct accounts%ROWTYPE; o orgs%ROWTYPE; d deployments%ROWTYPE;
BEGIN
 SELECT * INTO a FROM apps WHERE id=application_id FOR SHARE NOWAIT;
 IF NOT FOUND THEN
  RAISE EXCEPTION 'runtime application is missing' USING ERRCODE='23514',CONSTRAINT='application_standards_pending';
 END IF;
 SELECT * INTO o FROM orgs WHERE id=a.org_id FOR SHARE NOWAIT;
 SELECT * INTO acct FROM accounts WHERE id=a.account_id FOR SHARE NOWAIT;
 SELECT * INTO e FROM app_application_standards WHERE app_id=a.id FOR SHARE NOWAIT;
 IF NOT FOUND OR a.status='deleted' OR acct.status NOT IN ('active','past_due') OR acct.abuse_hold_at IS NOT NULL OR o.status NOT IN ('active','past_due') OR o.deleted_pending
   OR e.org_id IS DISTINCT FROM a.org_id OR e.project_id IS DISTINCT FROM a.project_id
   OR NOT ((e.state='unmanaged' AND e.adoptions='[]'::jsonb AND cardinality(e.materialized_fields)=0)
     OR (e.state IN ('persisted','observed') AND e.persisted_revision=e.desired_revision AND e.effective_hash <> '')) THEN
  RAISE EXCEPTION 'runtime application standards are incomplete' USING ERRCODE='23514',CONSTRAINT='application_standards_pending';
 END IF;
 IF NOT pg_try_advisory_xact_lock(hashtextextended('gregale.application-standard.controls.' || a.id::text,0)) THEN
  RAISE EXCEPTION 'runtime controls are busy' USING ERRCODE='55P03',CONSTRAINT='application_standard_runtime_busy';
 END IF;
 IF artifact_id IS NOT NULL THEN
  SELECT * INTO d FROM deployments WHERE id=artifact_id FOR SHARE NOWAIT;
  IF NOT FOUND OR d.app_id IS DISTINCT FROM a.id THEN
   RAISE EXCEPTION 'runtime artifact belongs to another application' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_identity';
  END IF;
  IF NOT pg_try_advisory_xact_lock(hashtextextended('gregale.application-standard.artifact-children.' || d.id::text,0)) THEN
   RAISE EXCEPTION 'runtime artifact children are busy' USING ERRCODE='55P03',CONSTRAINT='application_standard_runtime_busy';
  END IF;
 END IF;
 RETURN jsonb_build_object(
  'app_id',a.id::text,'org_id',a.org_id::text,'project_id',coalesce(a.project_id::text,''),'account_id',a.account_id::text,
  'account_plan',acct.plan,'account_egress_allowlist_extra',acct.egress_allowlist_extra,
  'desired_revision',e.desired_revision,'persisted_revision',e.persisted_revision,'effective_hash',e.effective_hash,
  'adoptions',e.adoptions,'materialized_fields',to_jsonb(e.materialized_fields),'effective',e.effective,
  'base_settings',e.base_settings,'local_settings',e.local_settings,'additional_log_destinations',to_jsonb(e.additional_log_destinations::text[]),
  'settings',jsonb_build_object('require_signed',a.require_signed,'security_policy',a.security_policy,
    'egress_cidrs',to_jsonb(a.egress_allowlist::text[]),'egress_extra_ports',to_jsonb(a.egress_ports)),
  'runtime',jsonb_build_object('type',a.type,'runtime',coalesce(a.runtime,''),'ram_mb',a.ram_mb,'app_protocol',a.app_protocol),
  'drains',coalesce((SELECT jsonb_agg(jsonb_build_object('id',r.id::text,'kind',r.kind,'enabled',r.enabled,
    'target_hash',encode(sha256(convert_to(r.target_url,'UTF8')),'hex'),
    'auth_hash',encode(sha256(coalesce(r.auth_header_sealed,''::bytea)),'hex')) ORDER BY r.id)
    FROM app_log_drains r WHERE r.app_id=a.id),'[]'::jsonb),
  'signers',coalesce((SELECT jsonb_agg(jsonb_build_object('name',r.signer_name,
    'fingerprint',encode(sha256(r.cosign_public_key),'hex')) ORDER BY r.signer_name)
    FROM app_trusted_signers r WHERE r.app_id=a.id),'[]'::jsonb),
  'artifact',CASE WHEN artifact_id IS NULL THEN '{}'::jsonb ELSE jsonb_build_object(
    'id',d.id::text,'scope',d.scope,'kind',d.kind,'image_digest',coalesce(d.image_digest,''),
    'rootfs_key',coalesce(d.rootfs_key,''),'rootfs_path',coalesce(d.rootfs_path,''),'rootfs_bytes',coalesce(d.rootfs_bytes,0),
    'source_sha256',coalesce(d.source_sha256,''),'parked_reason',coalesce(d.parked_reason,''),
    'scan_status',d.scan_status,'scan_result_hash',encode(sha256(convert_to(coalesce(d.scan_result::text,''),'UTF8')),'hex'),
    'sidecars',coalesce((SELECT jsonb_agg(jsonb_build_object('sidecar_name',r.sidecar_name,
      'storage_key',r.storage_key,'bytes',r.bytes,'content_digest',r.content_digest) ORDER BY r.sidecar_name)
      FROM deployment_sidecar_layers r WHERE r.deployment_id=d.id),'[]'::jsonb)) END);
EXCEPTION WHEN lock_not_available THEN
 RAISE EXCEPTION 'runtime inputs are busy' USING ERRCODE='55P03',CONSTRAINT='application_standard_runtime_busy';
END;
$$;
DROP FUNCTION application_standard_runtime_producers(apps,deployments);
DROP FUNCTION application_standard_runtime_base_producer(jsonb);
DROP FUNCTION application_standard_runtime_root_producer(apps,deployments,text);
-- +goose StatementEnd
