-- filename: 20261003232510425_application_standard_source_native_error_codes.sql
-- adr: 435. Translate source owner fences into the native admission error vocabulary.
-- +goose Up
-- +goose StatementBegin
CREATE FUNCTION application_standard_lock_source_runtime_rootfs(input jsonb, artifact_id uuid) RETURNS void
LANGUAGE plpgsql AS $$
DECLARE failed_constraint text;
BEGIN
 PERFORM lock_source_build_runtime_rootfs(input,artifact_id);
EXCEPTION WHEN check_violation THEN
 GET STACKED DIAGNOSTICS failed_constraint=CONSTRAINT_NAME;
 IF failed_constraint NOT IN ('build_export_publication_stale','build_export_publication_missing') THEN RAISE; END IF;
 RAISE EXCEPTION 'native source runtime inputs changed' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 WHEN lock_not_available THEN
 RAISE EXCEPTION 'native source runtime inputs busy' USING ERRCODE='55P03',CONSTRAINT='application_standard_runtime_busy';
END;
$$;

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
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
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
DROP FUNCTION application_standard_lock_source_runtime_rootfs(jsonb,uuid);
-- +goose StatementEnd
