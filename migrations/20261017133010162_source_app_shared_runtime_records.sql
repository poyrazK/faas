-- +goose Up
-- +goose StatementBegin
-- A source-built container app may record the shared runner base it was built
-- for (docs/adr/20261017-shared-runtime-source-apps.md, H10-140).
ALTER TABLE source_build_rootfs DROP CONSTRAINT source_build_rootfs_check1;
ALTER TABLE source_build_rootfs ADD CONSTRAINT source_build_rootfs_check1 CHECK((input_snapshot->>'publication_id'=publication_id::text AND input_snapshot->>'deployment_id'=deployment_id::text
  AND input_snapshot->>'account_id' ~ '^[a-f0-9-]{36}$' AND input_snapshot->>'app_id' ~ '^[a-f0-9-]{36}$'
  AND input_snapshot->>'publication_hash' ~ '^[a-f0-9]{64}$' AND input_snapshot->>'intent_hash' ~ '^[a-f0-9]{64}$'
  AND input_snapshot->>'base_producer_id' ~ '^[a-f0-9-]{36}$' AND input_snapshot->>'base_input_hash' ~ '^[a-f0-9]{64}$'
  AND input_snapshot->>'artifact_digest' ~ '^sha256:[a-f0-9]{64}$' AND (input_snapshot->>'artifact_bytes')::bigint>0
  AND (input_snapshot->>'content_bytes')::bigint>=0 AND coalesce(input_snapshot->>'storage_key','')<>''
  AND coalesce(input_snapshot->>'rootfs_path','')<>'' AND input_snapshot->>'guest_init_digest' ~ '^sha256:[a-f0-9]{64}$'
  AND input_snapshot->>'layout_version'='faas-app-layer-layout-v1'
  AND ((input_snapshot->>'kind'='source-app-layer' AND input_snapshot->>'runtime' IN ('','node22','node24') AND input_snapshot->>'runner_digest'='')
   OR (input_snapshot->>'kind'='function-layer' AND coalesce(input_snapshot->>'runtime','')<>''
    AND input_snapshot->>'runner_digest' ~ '^sha256:[a-f0-9]{64}$'))) IS TRUE);
CREATE OR REPLACE FUNCTION application_standard_source_runtime_producer(a apps, d deployments) RETURNS jsonb
LANGUAGE plpgsql AS $$
DECLARE f source_build_rootfs%ROWTYPE; origin build_export_publications%ROWTYPE;
 base jsonb; b base_image_producers%ROWTYPE; runtime text; kind text;
BEGIN
 SELECT r.* INTO f FROM source_build_rootfs_current c JOIN source_build_rootfs r ON r.id=c.artifact_id
 WHERE c.deployment_id=d.id FOR SHARE OF c,r NOWAIT;
 IF NOT FOUND THEN
  RAISE EXCEPTION 'source runtime selection missing' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 PERFORM application_standard_lock_source_runtime_rootfs(f.input_snapshot,f.id);
 SELECT * INTO origin FROM build_export_publications WHERE id=f.publication_id FOR SHARE NOWAIT;
 -- A container app without a runtime may run on a shared runner base its signed
 -- export claims (docs/adr/20261017-shared-runtime-source-apps.md).
 runtime:=CASE WHEN a.type='function' OR coalesce(a.runtime,'')<>'' THEN coalesce(nullif(a.runtime,''),d.handler,'')
  WHEN origin.input_snapshot->'claims'->>'runtime' IN ('node22','node24') THEN origin.input_snapshot->'claims'->>'runtime' ELSE '' END;
 kind:=CASE WHEN a.type='function' OR coalesce(a.runtime,'')<>'' THEN 'function-layer' ELSE 'source-app-layer' END;
 IF (origin.input_snapshot->'claims'->>'account_id'=a.account_id::text AND origin.input_snapshot->'claims'->>'app_id'=a.id::text
  AND origin.input_snapshot->'claims'->>'org_id'=coalesce(a.org_id::text,'')
  AND origin.input_snapshot->'claims'->>'deployment_id'=d.id::text
  AND origin.input_snapshot->'claims'->>'runtime'=CASE WHEN kind='source-app-layer' THEN runtime ELSE coalesce(a.runtime,'') END
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
  AND coalesce(a.org_id::text,'')=c->>'org_id' AND (coalesce(a.runtime,'')=c->>'runtime' OR (a.type<>'function' AND coalesce(a.runtime,'')='' AND c->>'runtime' IN ('node22','node24'))) AND a.status<>'deleted'
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
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE source_build_rootfs DROP CONSTRAINT source_build_rootfs_check1;
ALTER TABLE source_build_rootfs ADD CONSTRAINT source_build_rootfs_check1 CHECK((input_snapshot->>'publication_id'=publication_id::text AND input_snapshot->>'deployment_id'=deployment_id::text
  AND input_snapshot->>'account_id' ~ '^[a-f0-9-]{36}$' AND input_snapshot->>'app_id' ~ '^[a-f0-9-]{36}$'
  AND input_snapshot->>'publication_hash' ~ '^[a-f0-9]{64}$' AND input_snapshot->>'intent_hash' ~ '^[a-f0-9]{64}$'
  AND input_snapshot->>'base_producer_id' ~ '^[a-f0-9-]{36}$' AND input_snapshot->>'base_input_hash' ~ '^[a-f0-9]{64}$'
  AND input_snapshot->>'artifact_digest' ~ '^sha256:[a-f0-9]{64}$' AND (input_snapshot->>'artifact_bytes')::bigint>0
  AND (input_snapshot->>'content_bytes')::bigint>=0 AND coalesce(input_snapshot->>'storage_key','')<>''
  AND coalesce(input_snapshot->>'rootfs_path','')<>'' AND input_snapshot->>'guest_init_digest' ~ '^sha256:[a-f0-9]{64}$'
  AND input_snapshot->>'layout_version'='faas-app-layer-layout-v1'
  AND ((input_snapshot->>'kind'='source-app-layer' AND input_snapshot->>'runtime'='' AND input_snapshot->>'runner_digest'='')
   OR (input_snapshot->>'kind'='function-layer' AND coalesce(input_snapshot->>'runtime','')<>''
    AND input_snapshot->>'runner_digest' ~ '^sha256:[a-f0-9]{64}$'))) IS TRUE);
CREATE OR REPLACE FUNCTION application_standard_source_runtime_producer(a apps, d deployments) RETURNS jsonb
LANGUAGE plpgsql AS $$
DECLARE f source_build_rootfs%ROWTYPE; origin build_export_publications%ROWTYPE;
 base jsonb; b base_image_producers%ROWTYPE; runtime text; kind text;
BEGIN
 SELECT r.* INTO f FROM source_build_rootfs_current c JOIN source_build_rootfs r ON r.id=c.artifact_id
 WHERE c.deployment_id=d.id FOR SHARE OF c,r NOWAIT;
 IF NOT FOUND THEN
  RAISE EXCEPTION 'source runtime selection missing' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 PERFORM application_standard_lock_source_runtime_rootfs(f.input_snapshot,f.id);
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
-- +goose StatementEnd
