-- filename: 20261003221055458_application_standard_source_runtime_inputs.sql
-- adr: 435. Distinct source scanner bootstrap; native source authority remains refused.
-- +goose Up
-- +goose StatementBegin
CREATE FUNCTION lock_source_build_runtime_rootfs(input jsonb, expected_artifact uuid) RETURNS jsonb LANGUAGE plpgsql AS $$
DECLARE f source_build_rootfs%ROWTYPE; p build_export_publications%ROWTYPE; a apps%ROWTYPE; d deployments%ROWTYPE; b builds%ROWTYPE;
BEGIN
 SELECT r.* INTO f FROM source_build_rootfs_current c JOIN source_build_rootfs r ON r.id=c.artifact_id
 WHERE c.artifact_id=expected_artifact AND c.deployment_id=(input->>'deployment_id')::uuid FOR SHARE OF c,r NOWAIT;
 IF NOT FOUND OR f.input_snapshot IS DISTINCT FROM input THEN
  RAISE EXCEPTION 'source runtime selection changed' USING ERRCODE='23514',CONSTRAINT='build_export_publication_stale';
 END IF;
 SELECT * INTO p FROM build_export_publications WHERE id=f.publication_id FOR SHARE NOWAIT;
 SELECT * INTO d FROM deployments WHERE id=f.deployment_id FOR UPDATE NOWAIT;
 SELECT * INTO a FROM apps WHERE id=d.app_id FOR SHARE NOWAIT;
 -- Fence queued claims and new FK children as well as the successful build.
 PERFORM id FROM builds WHERE deployment_id=d.id ORDER BY id FOR SHARE NOWAIT;
 SELECT * INTO b FROM builds WHERE deployment_id=d.id ORDER BY started_at DESC NULLS LAST,id DESC LIMIT 1 FOR SHARE NOWAIT;
 IF (p.deployment_id=d.id AND p.app_id=a.id AND p.account_id=a.account_id AND a.status<>'deleted'
  AND p.build_id=b.id AND d.build_id=p.build_id AND p.input_hash=input->>'publication_hash'
  AND a.id::text=input->>'app_id' AND a.account_id::text=input->>'account_id'
  AND coalesce(a.org_id::text,'')=input->>'org_id' AND input->>'scope'=d.scope
  AND input->>'storage_key'=d.rootfs_key AND input->>'rootfs_path'=d.rootfs_path
  AND (input->>'content_bytes')::bigint=d.rootfs_bytes) IS NOT TRUE THEN
  RAISE EXCEPTION 'source runtime inputs changed' USING ERRCODE='23514',CONSTRAINT='build_export_publication_stale';
 END IF;
 -- This is an owner fence. Go authenticates the latest exact-claim proof under
 -- current controls and verifies intent/base hashes before publishing a scan.
 RETURN jsonb_build_object('intent',source_build_rootfs_intent(a,d));
EXCEPTION WHEN lock_not_available THEN
 RAISE EXCEPTION 'source runtime inputs busy' USING ERRCODE='55P03',CONSTRAINT='build_export_publication_busy';
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
-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
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
DROP FUNCTION lock_source_build_runtime_rootfs(jsonb,uuid);
-- +goose StatementEnd
