-- filename: 20261004011116969_application_standard_publisher_identity.sql
-- adr: 435. Resolve approved publisher identity by canonical DER fingerprint.
-- The existing owner/control/artifact fences remain unchanged. These functions
-- select current scoped bytes; the private Go stores verify actual P256 signatures.
-- The legacy publisher argument is a fingerprint for registry verification;
-- source publication derives its identity from the retained input proof.

-- +goose Up
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION lock_build_export_publication(input jsonb,publisher text,fresh boolean) RETURNS jsonb LANGUAGE plpgsql AS $$
DECLARE c jsonb:=input->'claims';a apps%ROWTYPE;d deployments%ROWTYPE;b builds%ROWTYPE;p build_provenance%ROWTYPE;key_der bytea;
BEGIN
 SELECT * INTO d FROM deployments WHERE id=(c->>'deployment_id')::uuid FOR SHARE NOWAIT;
 IF NOT FOUND THEN RAISE EXCEPTION 'build export owner missing' USING ERRCODE='23514',CONSTRAINT='build_export_publication_missing';END IF;
 SELECT * INTO a FROM apps WHERE id=(c->>'app_id')::uuid FOR SHARE NOWAIT;
 IF NOT FOUND THEN RAISE EXCEPTION 'build export owner missing' USING ERRCODE='23514',CONSTRAINT='build_export_publication_missing';END IF;
 -- The build lock serializes publication for one exact claim. No parent wait.
 SELECT * INTO b FROM builds WHERE id=(c->>'build_id')::uuid FOR UPDATE NOWAIT;
 IF NOT FOUND THEN RAISE EXCEPTION 'build export claim missing' USING ERRCODE='23514',CONSTRAINT='build_export_publication_missing';END IF;
 IF (a.id=d.app_id AND b.deployment_id=d.id AND a.account_id::text=c->>'account_id'
  AND coalesce(a.org_id::text,'')=c->>'org_id' AND coalesce(a.runtime,'')=c->>'runtime' AND a.status<>'deleted'
  AND b.started_at=(c->>'claim_started_at')::timestamptz
  AND d.kind IN ('tarball','dockerfile','github','preview')
  AND d.status IN ('pending','building','imaging','snapshotting','live','superseded')
  AND (coalesce(d.source_sha256,'')='' OR d.source_sha256=c->>'source_sha256')) IS NOT TRUE THEN
  RAISE EXCEPTION 'build export owner changed' USING ERRCODE='23514',CONSTRAINT='build_export_publication_stale';
 END IF;
 IF NOT pg_try_advisory_xact_lock(hashtextextended('gregale.application-standard.controls.'||a.id::text,0))
  OR NOT pg_try_advisory_xact_lock(hashtextextended('gregale.application-standard.artifact-children.'||d.id::text,0)) THEN
  RAISE EXCEPTION 'build export inputs busy' USING ERRCODE='55P03',CONSTRAINT='build_export_publication_busy';
 END IF;
 IF fresh OR b.status='succeeded' THEN
  SELECT * INTO p FROM build_provenance WHERE build_id=b.id FOR SHARE NOWAIT;
  IF (b.status='succeeded' AND p.started_at=b.started_at AND p.source_sha256=c->>'source_sha256'
   AND coalesce(p.builder_node_id,'')=c->>'builder_node_id') IS NOT TRUE THEN
   RAISE EXCEPTION 'build export completion changed' USING ERRCODE='23514',CONSTRAINT='build_export_publication_stale';
  END IF;
 ELSIF b.status<>'running' THEN
  RAISE EXCEPTION 'build export claim changed' USING ERRCODE='23514',CONSTRAINT='build_export_publication_stale';
 END IF;
 SELECT cosign_public_key INTO key_der FROM app_trusted_signers WHERE app_id=a.id AND account_id=a.account_id AND encode(sha256(cosign_public_key),'hex')=input->'proof'->>'publisher_key_sha256' ORDER BY signer_name LIMIT 1;
 RETURN jsonb_build_object('key_der',coalesce(encode(key_der,'base64'),''),'checked_at',clock_timestamp());
EXCEPTION WHEN lock_not_available THEN
 RAISE EXCEPTION 'build export inputs busy' USING ERRCODE='55P03',CONSTRAINT='build_export_publication_busy';
END;
$$;

CREATE OR REPLACE FUNCTION lock_deployment_registry_verification(application_id uuid, artifact_id uuid, owner_id uuid, workload text, publisher text)
RETURNS jsonb LANGUAGE plpgsql AS $$
DECLARE a apps%ROWTYPE; d deployments%ROWTYPE; image_ref text; key_der bytea; matches integer;
BEGIN
 SELECT * INTO d FROM deployments WHERE id=artifact_id FOR SHARE NOWAIT;
 IF NOT FOUND THEN
  RAISE EXCEPTION 'registry artifact missing' USING ERRCODE='23514',CONSTRAINT='deployment_registry_verification_missing';
 END IF;
 SELECT * INTO a FROM apps WHERE id=application_id FOR SHARE NOWAIT;
 IF NOT FOUND THEN
  RAISE EXCEPTION 'registry application missing' USING ERRCODE='23514',CONSTRAINT='deployment_registry_verification_missing';
 END IF;
 IF a.id IS DISTINCT FROM d.app_id OR a.account_id IS DISTINCT FROM owner_id OR a.status='deleted'
   OR d.status NOT IN ('pending','building','imaging','snapshotting','live','superseded') THEN
  RAISE EXCEPTION 'registry artifact scope changed' USING ERRCODE='23514',CONSTRAINT='deployment_registry_verification_stale';
 END IF;
 IF NOT pg_try_advisory_xact_lock(hashtextextended('gregale.application-standard.controls.' || a.id::text,0))
   OR NOT pg_try_advisory_xact_lock(hashtextextended('gregale.application-standard.artifact-children.' || d.id::text,0)) THEN
  RAISE EXCEPTION 'registry verification inputs busy' USING ERRCODE='55P03',CONSTRAINT='deployment_registry_verification_busy';
 END IF;
 IF workload='' AND d.kind='image' THEN
  image_ref := d.image_digest;
 ELSIF workload <> '' THEN
  SELECT count(*),min(s->>'image') INTO matches,image_ref FROM jsonb_array_elements(d.sidecars) s WHERE s->>'name'=workload;
  IF matches <> 1 THEN image_ref := NULL; END IF;
 END IF;
 IF image_ref IS NULL OR image_ref='' THEN
  RAISE EXCEPTION 'registry workload missing' USING ERRCODE='23514',CONSTRAINT='deployment_registry_verification_stale';
 END IF;
 SELECT cosign_public_key INTO key_der FROM app_trusted_signers WHERE app_id=a.id AND account_id=a.account_id AND encode(sha256(cosign_public_key),'hex')=publisher ORDER BY signer_name LIMIT 1;
 RETURN jsonb_build_object('app_id',a.id::text,'account_id',a.account_id::text,'org_id',coalesce(a.org_id::text,''),
   'image_reference',image_ref,'key_der',coalesce(encode(key_der,'base64'),''));
EXCEPTION WHEN lock_not_available THEN
 RAISE EXCEPTION 'registry verification inputs busy' USING ERRCODE='55P03',CONSTRAINT='deployment_registry_verification_busy';
END;
$$;

CREATE OR REPLACE FUNCTION lock_deployment_registry_rootfs(verification_id uuid) RETURNS jsonb LANGUAGE plpgsql AS $$
DECLARE r deployment_registry_verifications%ROWTYPE; d deployments%ROWTYPE; owner_inputs jsonb;
BEGIN
 SELECT * INTO r FROM deployment_registry_verifications WHERE id=verification_id;
 IF NOT FOUND THEN
  RAISE EXCEPTION 'registry verification missing' USING ERRCODE='23514',CONSTRAINT='deployment_registry_verification_missing';
 END IF;
 SELECT * INTO d FROM deployments WHERE id=r.deployment_id FOR UPDATE NOWAIT;
 IF NOT FOUND OR d.status NOT IN ('pending','building','imaging','snapshotting') THEN
  RAISE EXCEPTION 'registry conversion is no longer active' USING ERRCODE='23514',CONSTRAINT='deployment_registry_verification_stale';
 END IF;
 owner_inputs:=lock_deployment_registry_verification(r.app_id,r.deployment_id,r.account_id,r.workload_name,r.input_snapshot->'proof'->>'PublisherKeySHA256');
 RETURN owner_inputs || jsonb_build_object('scope',d.scope,'status',d.status,'storage_now',clock_timestamp());
EXCEPTION WHEN lock_not_available THEN
 RAISE EXCEPTION 'registry rootfs inputs busy' USING ERRCODE='55P03',CONSTRAINT='deployment_registry_verification_busy';
END;
$$;

CREATE OR REPLACE FUNCTION lock_deployment_artifact_scan(producer_id uuid) RETURNS jsonb LANGUAGE plpgsql AS $$
DECLARE f deployment_registry_rootfs%ROWTYPE; r deployment_registry_verifications%ROWTYPE; d deployments%ROWTYPE; owner_inputs jsonb;
BEGIN
 SELECT * INTO f FROM deployment_registry_rootfs WHERE id=producer_id;
 IF NOT FOUND THEN RAISE EXCEPTION 'rootfs producer missing' USING ERRCODE='23514',CONSTRAINT='deployment_registry_verification_missing'; END IF;
 SELECT * INTO r FROM deployment_registry_verifications WHERE id=f.registry_verification_id;
 SELECT * INTO d FROM deployments WHERE id=f.deployment_id FOR UPDATE NOWAIT;
 IF NOT FOUND THEN RAISE EXCEPTION 'scan deployment missing' USING ERRCODE='23514',CONSTRAINT='deployment_registry_verification_missing'; END IF;
 owner_inputs:=lock_deployment_registry_verification(r.app_id,r.deployment_id,r.account_id,r.workload_name,r.input_snapshot->'proof'->>'PublisherKeySHA256');
 RETURN owner_inputs || jsonb_build_object('scope',d.scope,'status',d.status,'storage_now',clock_timestamp());
EXCEPTION WHEN lock_not_available THEN
 RAISE EXCEPTION 'artifact scan inputs busy' USING ERRCODE='55P03',CONSTRAINT='deployment_registry_verification_busy';
END;
$$;

CREATE OR REPLACE FUNCTION lock_deployment_artifact_scan_with_verification(producer_id uuid, verification_id uuid) RETURNS jsonb LANGUAGE plpgsql AS $$
DECLARE f deployment_registry_rootfs%ROWTYPE; r deployment_registry_verifications%ROWTYPE; d deployments%ROWTYPE; owner_inputs jsonb;
BEGIN
 SELECT * INTO f FROM deployment_registry_rootfs WHERE id=producer_id;
 IF NOT FOUND THEN RAISE EXCEPTION 'rootfs producer missing' USING ERRCODE='23514',CONSTRAINT='deployment_registry_verification_missing'; END IF;
 SELECT * INTO r FROM deployment_registry_verifications WHERE id=coalesce(verification_id,f.registry_verification_id);
 IF NOT FOUND THEN RAISE EXCEPTION 'scan verification missing' USING ERRCODE='23514',CONSTRAINT='deployment_registry_verification_missing'; END IF;
 IF r.deployment_id<>f.deployment_id OR r.workload_name<>f.workload_name THEN
  RAISE EXCEPTION 'scan approval workload changed' USING ERRCODE='23514',CONSTRAINT='deployment_registry_verification_stale';
 END IF;
 SELECT * INTO d FROM deployments WHERE id=f.deployment_id FOR UPDATE NOWAIT;
 IF NOT FOUND THEN RAISE EXCEPTION 'scan deployment missing' USING ERRCODE='23514',CONSTRAINT='deployment_registry_verification_missing'; END IF;
 owner_inputs:=lock_deployment_registry_verification(r.app_id,r.deployment_id,r.account_id,r.workload_name,r.input_snapshot->'proof'->>'PublisherKeySHA256');
 RETURN owner_inputs || jsonb_build_object('scope',d.scope,'status',d.status,'storage_now',clock_timestamp());
EXCEPTION WHEN lock_not_available THEN
 RAISE EXCEPTION 'artifact scan renewal inputs busy' USING ERRCODE='55P03',CONSTRAINT='deployment_registry_verification_busy';
END;
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION lock_build_export_publication(input jsonb,publisher text,fresh boolean) RETURNS jsonb LANGUAGE plpgsql AS $$
DECLARE c jsonb:=input->'claims';a apps%ROWTYPE;d deployments%ROWTYPE;b builds%ROWTYPE;p build_provenance%ROWTYPE;key_der bytea;
BEGIN
 SELECT * INTO d FROM deployments WHERE id=(c->>'deployment_id')::uuid FOR SHARE NOWAIT;
 IF NOT FOUND THEN RAISE EXCEPTION 'build export owner missing' USING ERRCODE='23514',CONSTRAINT='build_export_publication_missing';END IF;
 SELECT * INTO a FROM apps WHERE id=(c->>'app_id')::uuid FOR SHARE NOWAIT;
 IF NOT FOUND THEN RAISE EXCEPTION 'build export owner missing' USING ERRCODE='23514',CONSTRAINT='build_export_publication_missing';END IF;
 -- The build lock serializes publication for one exact claim. No parent wait.
 SELECT * INTO b FROM builds WHERE id=(c->>'build_id')::uuid FOR UPDATE NOWAIT;
 IF NOT FOUND THEN RAISE EXCEPTION 'build export claim missing' USING ERRCODE='23514',CONSTRAINT='build_export_publication_missing';END IF;
 IF (a.id=d.app_id AND b.deployment_id=d.id AND a.account_id::text=c->>'account_id'
  AND coalesce(a.org_id::text,'')=c->>'org_id' AND coalesce(a.runtime,'')=c->>'runtime' AND a.status<>'deleted'
  AND b.started_at=(c->>'claim_started_at')::timestamptz
  AND d.kind IN ('tarball','dockerfile','github','preview')
  AND d.status IN ('pending','building','imaging','snapshotting','live','superseded')
  AND (coalesce(d.source_sha256,'')='' OR d.source_sha256=c->>'source_sha256')) IS NOT TRUE THEN
  RAISE EXCEPTION 'build export owner changed' USING ERRCODE='23514',CONSTRAINT='build_export_publication_stale';
 END IF;
 IF NOT pg_try_advisory_xact_lock(hashtextextended('gregale.application-standard.controls.'||a.id::text,0))
  OR NOT pg_try_advisory_xact_lock(hashtextextended('gregale.application-standard.artifact-children.'||d.id::text,0)) THEN
  RAISE EXCEPTION 'build export inputs busy' USING ERRCODE='55P03',CONSTRAINT='build_export_publication_busy';
 END IF;
 IF fresh OR b.status='succeeded' THEN
  SELECT * INTO p FROM build_provenance WHERE build_id=b.id FOR SHARE NOWAIT;
  IF (b.status='succeeded' AND p.started_at=b.started_at AND p.source_sha256=c->>'source_sha256'
   AND coalesce(p.builder_node_id,'')=c->>'builder_node_id') IS NOT TRUE THEN
   RAISE EXCEPTION 'build export completion changed' USING ERRCODE='23514',CONSTRAINT='build_export_publication_stale';
  END IF;
 ELSIF b.status<>'running' THEN
  RAISE EXCEPTION 'build export claim changed' USING ERRCODE='23514',CONSTRAINT='build_export_publication_stale';
 END IF;
 SELECT cosign_public_key INTO key_der FROM app_trusted_signers WHERE app_id=a.id AND account_id=a.account_id AND signer_name=publisher;
 RETURN jsonb_build_object('key_der',coalesce(encode(key_der,'base64'),''),'checked_at',clock_timestamp());
EXCEPTION WHEN lock_not_available THEN
 RAISE EXCEPTION 'build export inputs busy' USING ERRCODE='55P03',CONSTRAINT='build_export_publication_busy';
END;
$$;

CREATE OR REPLACE FUNCTION lock_deployment_registry_verification(application_id uuid, artifact_id uuid, owner_id uuid, workload text, publisher text)
RETURNS jsonb LANGUAGE plpgsql AS $$
DECLARE a apps%ROWTYPE; d deployments%ROWTYPE; image_ref text; key_der bytea; matches integer;
BEGIN
 SELECT * INTO d FROM deployments WHERE id=artifact_id FOR SHARE NOWAIT;
 IF NOT FOUND THEN
  RAISE EXCEPTION 'registry artifact missing' USING ERRCODE='23514',CONSTRAINT='deployment_registry_verification_missing';
 END IF;
 SELECT * INTO a FROM apps WHERE id=application_id FOR SHARE NOWAIT;
 IF NOT FOUND THEN
  RAISE EXCEPTION 'registry application missing' USING ERRCODE='23514',CONSTRAINT='deployment_registry_verification_missing';
 END IF;
 IF a.id IS DISTINCT FROM d.app_id OR a.account_id IS DISTINCT FROM owner_id OR a.status='deleted'
   OR d.status NOT IN ('pending','building','imaging','snapshotting','live','superseded') THEN
  RAISE EXCEPTION 'registry artifact scope changed' USING ERRCODE='23514',CONSTRAINT='deployment_registry_verification_stale';
 END IF;
 IF NOT pg_try_advisory_xact_lock(hashtextextended('gregale.application-standard.controls.' || a.id::text,0))
   OR NOT pg_try_advisory_xact_lock(hashtextextended('gregale.application-standard.artifact-children.' || d.id::text,0)) THEN
  RAISE EXCEPTION 'registry verification inputs busy' USING ERRCODE='55P03',CONSTRAINT='deployment_registry_verification_busy';
 END IF;
 IF workload='' AND d.kind='image' THEN
  image_ref := d.image_digest;
 ELSIF workload <> '' THEN
  SELECT count(*),min(s->>'image') INTO matches,image_ref FROM jsonb_array_elements(d.sidecars) s WHERE s->>'name'=workload;
  IF matches <> 1 THEN image_ref := NULL; END IF;
 END IF;
 IF image_ref IS NULL OR image_ref='' THEN
  RAISE EXCEPTION 'registry workload missing' USING ERRCODE='23514',CONSTRAINT='deployment_registry_verification_stale';
 END IF;
 SELECT cosign_public_key INTO key_der FROM app_trusted_signers WHERE app_id=a.id AND signer_name=publisher AND account_id=a.account_id;
 RETURN jsonb_build_object('app_id',a.id::text,'account_id',a.account_id::text,'org_id',coalesce(a.org_id::text,''),
   'image_reference',image_ref,'key_der',coalesce(encode(key_der,'base64'),''));
EXCEPTION WHEN lock_not_available THEN
 RAISE EXCEPTION 'registry verification inputs busy' USING ERRCODE='55P03',CONSTRAINT='deployment_registry_verification_busy';
END;
$$;

CREATE OR REPLACE FUNCTION lock_deployment_registry_rootfs(verification_id uuid) RETURNS jsonb LANGUAGE plpgsql AS $$
DECLARE r deployment_registry_verifications%ROWTYPE; d deployments%ROWTYPE; owner_inputs jsonb;
BEGIN
 SELECT * INTO r FROM deployment_registry_verifications WHERE id=verification_id;
 IF NOT FOUND THEN
  RAISE EXCEPTION 'registry verification missing' USING ERRCODE='23514',CONSTRAINT='deployment_registry_verification_missing';
 END IF;
 SELECT * INTO d FROM deployments WHERE id=r.deployment_id FOR UPDATE NOWAIT;
 IF NOT FOUND OR d.status NOT IN ('pending','building','imaging','snapshotting') THEN
  RAISE EXCEPTION 'registry conversion is no longer active' USING ERRCODE='23514',CONSTRAINT='deployment_registry_verification_stale';
 END IF;
 owner_inputs:=lock_deployment_registry_verification(r.app_id,r.deployment_id,r.account_id,r.workload_name,r.input_snapshot->'proof'->>'PublisherName');
 RETURN owner_inputs || jsonb_build_object('scope',d.scope,'status',d.status,'storage_now',clock_timestamp());
EXCEPTION WHEN lock_not_available THEN
 RAISE EXCEPTION 'registry rootfs inputs busy' USING ERRCODE='55P03',CONSTRAINT='deployment_registry_verification_busy';
END;
$$;

CREATE OR REPLACE FUNCTION lock_deployment_artifact_scan(producer_id uuid) RETURNS jsonb LANGUAGE plpgsql AS $$
DECLARE f deployment_registry_rootfs%ROWTYPE; r deployment_registry_verifications%ROWTYPE; d deployments%ROWTYPE; owner_inputs jsonb;
BEGIN
 SELECT * INTO f FROM deployment_registry_rootfs WHERE id=producer_id;
 IF NOT FOUND THEN RAISE EXCEPTION 'rootfs producer missing' USING ERRCODE='23514',CONSTRAINT='deployment_registry_verification_missing'; END IF;
 SELECT * INTO r FROM deployment_registry_verifications WHERE id=f.registry_verification_id;
 SELECT * INTO d FROM deployments WHERE id=f.deployment_id FOR UPDATE NOWAIT;
 IF NOT FOUND THEN RAISE EXCEPTION 'scan deployment missing' USING ERRCODE='23514',CONSTRAINT='deployment_registry_verification_missing'; END IF;
 owner_inputs:=lock_deployment_registry_verification(r.app_id,r.deployment_id,r.account_id,r.workload_name,r.input_snapshot->'proof'->>'PublisherName');
 RETURN owner_inputs || jsonb_build_object('scope',d.scope,'status',d.status,'storage_now',clock_timestamp());
EXCEPTION WHEN lock_not_available THEN
 RAISE EXCEPTION 'artifact scan inputs busy' USING ERRCODE='55P03',CONSTRAINT='deployment_registry_verification_busy';
END;
$$;

CREATE OR REPLACE FUNCTION lock_deployment_artifact_scan_with_verification(producer_id uuid, verification_id uuid) RETURNS jsonb LANGUAGE plpgsql AS $$
DECLARE f deployment_registry_rootfs%ROWTYPE; r deployment_registry_verifications%ROWTYPE; d deployments%ROWTYPE; owner_inputs jsonb;
BEGIN
 SELECT * INTO f FROM deployment_registry_rootfs WHERE id=producer_id;
 IF NOT FOUND THEN RAISE EXCEPTION 'rootfs producer missing' USING ERRCODE='23514',CONSTRAINT='deployment_registry_verification_missing'; END IF;
 SELECT * INTO r FROM deployment_registry_verifications WHERE id=coalesce(verification_id,f.registry_verification_id);
 IF NOT FOUND THEN RAISE EXCEPTION 'scan verification missing' USING ERRCODE='23514',CONSTRAINT='deployment_registry_verification_missing'; END IF;
 IF r.deployment_id<>f.deployment_id OR r.workload_name<>f.workload_name THEN
  RAISE EXCEPTION 'scan approval workload changed' USING ERRCODE='23514',CONSTRAINT='deployment_registry_verification_stale';
 END IF;
 SELECT * INTO d FROM deployments WHERE id=f.deployment_id FOR UPDATE NOWAIT;
 IF NOT FOUND THEN RAISE EXCEPTION 'scan deployment missing' USING ERRCODE='23514',CONSTRAINT='deployment_registry_verification_missing'; END IF;
 owner_inputs:=lock_deployment_registry_verification(r.app_id,r.deployment_id,r.account_id,r.workload_name,r.input_snapshot->'proof'->>'PublisherName');
 RETURN owner_inputs || jsonb_build_object('scope',d.scope,'status',d.status,'storage_now',clock_timestamp());
EXCEPTION WHEN lock_not_available THEN
 RAISE EXCEPTION 'artifact scan renewal inputs busy' USING ERRCODE='55P03',CONSTRAINT='deployment_registry_verification_busy';
END;
$$;
-- +goose StatementEnd
