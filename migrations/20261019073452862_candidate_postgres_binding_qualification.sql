-- filename: 20261019073452862_candidate_postgres_binding_qualification.sql
-- ADR-20261019072523: control-plane credential receipts admit managed
-- PostgreSQL bindings into candidate binding qualification.
-- migration-compat: expand

-- +goose Up
SET LOCAL lock_timeout = '5s';
-- One immutable receipt per verified credential delivery. apid writes it after
-- decrypting the exact sealed secret and running the read-only ACL probe.
CREATE TABLE IF NOT EXISTS environment_qualification_postgres_receipts (
 request_id uuid NOT NULL REFERENCES environment_workload_qualification_requests(id) ON DELETE CASCADE,
 attempt bigint NOT NULL CHECK(attempt>0),
 binding_id uuid NOT NULL,
 environment_key text NOT NULL CHECK(environment_key ~ '^[A-Z_][A-Z0-9_]{0,126}$'),
 access text NOT NULL CHECK(access IN ('read_write','read_only','migration')),
 credential_generation bigint NOT NULL CHECK(credential_generation>=1),
 delivery_version bigint NOT NULL CHECK(delivery_version>=1),
 recorded_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(request_id,attempt,binding_id,credential_generation,delivery_version)
);

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_environment_qualification_postgres_receipt() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP<>'INSERT' THEN
  IF TG_OP='DELETE' AND pg_trigger_depth()>1 THEN RETURN OLD; END IF;
  RAISE EXCEPTION 'candidate PostgreSQL receipts are immutable' USING ERRCODE='23514';
 END IF;
 IF NOT EXISTS(SELECT 1 FROM environment_workload_qualification_requests q
  JOIN environment_workload_graphs g ON g.id=q.graph_id
  JOIN project_environments e ON e.id=g.environment_id
  JOIN managed_postgres_bindings b ON b.id=NEW.binding_id AND b.app_id=q.app_id AND b.scope=e.slug
  JOIN app_secrets s ON s.app_id=q.app_id AND s.scope=e.slug AND s.key=b.environment_key
  WHERE q.id=NEW.request_id AND q.attempt=NEW.attempt AND q.binding_context IS NOT NULL AND q.lease_until>clock_timestamp()
   AND b.state='ready' AND b.rotation_previous_generation IS NULL AND b.cutover_id IS NULL
   AND b.access=NEW.access AND b.environment_key=NEW.environment_key AND b.credential_generation=NEW.credential_generation
   AND s.managed_postgres_binding_id=b.id AND s.managed_credential_generation=b.credential_generation
   AND s.delivery_version=NEW.delivery_version) THEN
  RAISE EXCEPTION 'candidate PostgreSQL receipt requires the current qualification lease and credential delivery' USING ERRCODE='23514',CONSTRAINT='binding_release_changed';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS guard_environment_qualification_postgres_receipt ON environment_qualification_postgres_receipts;
CREATE TRIGGER guard_environment_qualification_postgres_receipt BEFORE INSERT OR UPDATE OR DELETE ON environment_qualification_postgres_receipts
 FOR EACH ROW EXECUTE FUNCTION guard_environment_qualification_postgres_receipt();

-- Returns 'infinity' when the candidate has no PostgreSQL binding, NULL when a
-- binding is unsupported, unsettled or lacks a current receipt. Runtime
-- credentials must be the exact delivery the candidate was captured and
-- restored with; migration credentials are release-only (release task before prime).
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION environment_binding_postgres_deadline(q environment_workload_qualification_requests,release_scope text,max_age_seconds bigint,restored_instance uuid) RETURNS timestamptz LANGUAGE plpgsql STABLE AS $$
DECLARE b managed_postgres_bindings%ROWTYPE; s app_secrets%ROWTYPE; receipt environment_qualification_postgres_receipts%ROWTYPE;
 captured jsonb; restored jsonb; until_at timestamptz:='infinity';
BEGIN
 SELECT inputs->'secret_versions' INTO captured FROM environment_qualification_snapshot_receipts WHERE instance_id=q.reserved_instance_id;
 SELECT runtime_inputs->'secret_versions' INTO restored FROM environment_qualification_restore_receipts
  WHERE request_id=q.id AND attempt=q.attempt AND instance_id=restored_instance;
 FOR b IN SELECT * FROM managed_postgres_bindings WHERE app_id=q.app_id AND scope=release_scope AND state<>'deleted' ORDER BY id LOOP
  IF b.access NOT IN ('read_write','read_only','migration') OR b.state<>'ready' OR b.rotation_previous_generation IS NOT NULL OR b.cutover_id IS NOT NULL THEN RETURN NULL; END IF;
  SELECT * INTO s FROM app_secrets WHERE app_id=q.app_id AND scope=release_scope AND key=b.environment_key;
  IF s.managed_postgres_binding_id IS DISTINCT FROM b.id OR s.managed_credential_generation IS DISTINCT FROM b.credential_generation OR s.delivery_version IS NULL THEN RETURN NULL; END IF;
  SELECT * INTO receipt FROM environment_qualification_postgres_receipts WHERE request_id=q.id AND attempt=q.attempt AND binding_id=b.id
   AND credential_generation=b.credential_generation AND delivery_version=s.delivery_version AND environment_key=b.environment_key AND access=b.access
   ORDER BY recorded_at DESC LIMIT 1;
  IF receipt.request_id IS NULL OR receipt.recorded_at>clock_timestamp() THEN RETURN NULL; END IF;
  IF b.access<>'migration' AND ((captured->>(release_scope||'/'||b.environment_key)) IS DISTINCT FROM s.delivery_version::text OR
   (restored->>(release_scope||'/'||b.environment_key)) IS DISTINCT FROM s.delivery_version::text) THEN RETURN NULL; END IF;
  until_at:=least(until_at,receipt.recorded_at+max_age_seconds*interval '1 second');
 END LOOP;
 RETURN until_at;
END $$;
-- +goose StatementEnd

-- PostgreSQL is now qualified by receipts in the deadline below.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION environment_binding_catalog_supported(target_app uuid,target_scope text) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT NOT EXISTS(SELECT 1 FROM object_storage_s3_credentials WHERE managed_app_id=target_app AND managed_scope=target_scope AND status='active')
 AND NOT EXISTS(SELECT 1 FROM outbound_app_bindings WHERE app_id=target_app)
 AND NOT EXISTS(SELECT 1 FROM outbound_integration_apps WHERE app_id=target_app);
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION environment_binding_qualification_deadline(target_graph uuid) RETURNS timestamptz LANGUAGE plpgsql AS $$
DECLARE g environment_workload_graphs%ROWTYPE; s environment_git_sources%ROWTYPE; e project_environments%ROWTYPE;
 q environment_workload_qualification_requests%ROWTYPE; p app_binding_release_policies%ROWTYPE;
 smoke environment_qualification_smoke_receipts%ROWTYPE; edge record; target environment_workload_qualification_requests%ROWTYPE;
 receipt environment_qualification_service_receipts%ROWTYPE; until_at timestamptz; deadline timestamptz; postgres_deadline timestamptz; count_members integer:=0;
BEGIN
 SELECT * INTO g FROM environment_workload_graphs WHERE id=target_graph;
 SELECT * INTO s FROM active_environment_git_sources WHERE id=g.source_id;
 SELECT * INTO e FROM project_environments WHERE id=g.environment_id;
 IF g.id IS NULL OR s.id IS NULL OR s.mode<>'enforce' OR s.suspended OR g.generation<>s.generation OR
 g.intent_version<>s.intent_version OR g.revision_id<>s.approved_revision_id OR g.phase<>'prepared' THEN RETURN NULL; END IF;
 IF EXISTS(SELECT 1 FROM jsonb_array_elements(g.members) m WHERE coalesce(m->>'candidate_deployment_id','')='') THEN RETURN NULL; END IF;
 IF (SELECT count(*) FROM jsonb_array_elements(g.members))<>(SELECT count(*) FROM environment_workload_qualification_requests WHERE graph_id=g.id) THEN RETURN NULL; END IF;
 FOR q IN SELECT * FROM environment_workload_qualification_requests WHERE graph_id=g.id ORDER BY app_id LOOP
  count_members:=count_members+1;
  SELECT * INTO p FROM app_binding_release_policies WHERE app_id=q.app_id AND scope=e.slug;
  SELECT * INTO smoke FROM environment_qualification_smoke_receipts WHERE request_id=q.id AND attempt=q.attempt;
  IF q.binding_context IS NULL OR p.app_id IS NULL OR p.mode<>'enforce' OR p.require_application_ack OR p.object_storage_requirements<>'{}'::jsonb OR
   q.binding_context->'policy'->>'revision' IS DISTINCT FROM p.revision::text OR q.binding_context->'policy'->>'mode' IS DISTINCT FROM p.mode OR
   q.binding_context->'policy'->>'scope' IS DISTINCT FROM e.slug OR q.binding_context->'policy'->>'app_id' IS DISTINCT FROM q.app_id::text OR
   coalesce(q.binding_context->'policy'->'queue_requirements','{}'::jsonb) IS DISTINCT FROM p.queue_requirements OR
   coalesce(q.binding_context->'policy'->'object_storage_requirements','{}'::jsonb) IS DISTINCT FROM p.object_storage_requirements OR
   NOT environment_binding_catalog_supported(q.app_id,e.slug) OR NOT environment_workload_qualification_evidence_current(q.id) OR
   q.binding_context->'artifact_inputs' IS DISTINCT FROM (SELECT environment_binding_artifact_inputs(d) FROM deployments d WHERE d.id=q.deployment_id) OR
   q.binding_context->>'service_revision' IS DISTINCT FROM (SELECT epoch::text||':'||service_revision::text FROM app_binding_promotion_revisions WHERE app_id=q.app_id) OR
   EXISTS(SELECT 1 FROM queue_bindings b WHERE b.app_id=q.app_id AND coalesce(nullif(b.deployment_scope,''),'default') IN (e.slug,'default') AND b.retired_at IS NULL AND NOT EXISTS(SELECT 1 FROM jsonb_each(coalesce(q.frozen_inputs->'queue_bindings','{}'::jsonb)) f WHERE f.value->>'binding_id'=b.id::text)) OR
   q.execution_mode NOT IN ('request','service','worker') OR q.frozen_inputs->>'workload_class' NOT IN ('http','worker') OR
   smoke.request_id IS NULL OR smoke.recorded_at>clock_timestamp() OR q.lease_until IS NULL THEN RETURN NULL; END IF;
  IF NOT EXISTS(SELECT 1 FROM environment_qualification_snapshot_receipts capture
   JOIN environment_qualification_executions original ON original.instance_id=capture.instance_id
   JOIN environment_qualification_restore_receipts restored ON restored.request_id=q.id AND restored.attempt=q.attempt AND restored.capture_instance_id=capture.instance_id
   JOIN environment_qualification_executions runtime ON runtime.instance_id=restored.instance_id
   JOIN environment_qualification_framework_ready_receipts ready ON ready.request_id=q.id AND ready.attempt=q.attempt AND ready.instance_id=restored.instance_id
   JOIN environment_qualification_config_receipts source_config ON source_config.instance_id=capture.instance_id
   JOIN environment_qualification_config_receipts restore_config ON restore_config.instance_id=restored.instance_id
   WHERE capture.instance_id=q.reserved_instance_id AND restored.instance_id=smoke.instance_id AND smoke.graph_id=g.id AND smoke.capture_instance_id=q.reserved_instance_id
    AND original.dispatch_started AND runtime.dispatch_started AND original.retired_at IS NOT NULL AND runtime.retired_at IS NOT NULL
    AND original.retirement->>'kind'='native_retired' AND runtime.retirement->>'kind'='native_retired'
    AND environment_runtime_inputs_fresh(q.app_id,e.slug,(capture.inputs->>'boundary')::timestamptz,capture.inputs->'variables',capture.inputs->'secret_versions',(capture.inputs->>'all_secrets')::boolean,capture.inputs->'secret_refs',coalesce(capture.inputs->'sidecar_secret_versions','{}'::jsonb))
    AND environment_runtime_inputs_fresh(q.app_id,e.slug,(restored.runtime_inputs->>'boundary')::timestamptz,restored.runtime_inputs->'variables',restored.runtime_inputs->'secret_versions',(restored.runtime_inputs->>'all_secrets')::boolean,restored.runtime_inputs->'secret_refs',coalesce(restored.runtime_inputs->'sidecar_secret_versions','{}'::jsonb))) THEN RETURN NULL; END IF;
  IF coalesce(q.frozen_inputs->'service_bindings','{}'::jsonb)<>'{}'::jsonb AND
   coalesce(nullif(q.frozen_inputs->'runtime'->>'service_binding_transport',''),q.frozen_inputs->'baseline'->>'service_binding_transport','http')<>'https' THEN RETURN NULL; END IF;
  IF (SELECT count(*) FROM jsonb_each(coalesce(q.frozen_inputs->'queue_bindings','{}'::jsonb)))<>(SELECT count(*) FROM jsonb_each(p.queue_requirements)) OR
   EXISTS(SELECT 1 FROM jsonb_each(coalesce(q.frozen_inputs->'queue_bindings','{}'::jsonb)) b
    WHERE q.execution_mode<>'worker' OR b.value->'contract'->>'mode'<>'push' OR b.value->'contract'->>'enabled' IS DISTINCT FROM 'true' OR
     p.queue_requirements->>b.key IS DISTINCT FROM 'handler_completion' OR NOT coalesce(q.frozen_inputs->'queue_smoke','{}'::jsonb) ? b.key) THEN RETURN NULL; END IF;
  deadline:=least(q.lease_until,smoke.recorded_at+p.max_age_seconds*interval '1 second');
  -- LEAST ignores NULL, so a missing PostgreSQL receipt must stop here.
  postgres_deadline:=environment_binding_postgres_deadline(q,e.slug,p.max_age_seconds,smoke.instance_id);
  IF postgres_deadline IS NULL THEN RETURN NULL; END IF;
  deadline:=least(deadline,postgres_deadline);
  FOR edge IN SELECT key,value FROM jsonb_each(coalesce(q.frozen_inputs->'service_bindings','{}'::jsonb)) LOOP
   SELECT * INTO target FROM environment_workload_qualification_requests WHERE graph_id=g.id AND app_id=(edge.value->>'target_app_id')::uuid;
   SELECT * INTO receipt FROM environment_qualification_service_receipts WHERE request_id=q.id AND attempt=q.attempt AND binding=edge.key;
   IF target.id IS NULL OR receipt.request_id IS NULL OR receipt.recorded_at>smoke.recorded_at OR receipt.route->>'RequireHTTPS' IS DISTINCT FROM 'true' OR
    receipt.route->'Caller'->>'request_id' IS DISTINCT FROM q.id::text OR receipt.route->'Caller'->>'attempt' IS DISTINCT FROM q.attempt::text OR
    receipt.route->'Caller'->>'instance_id' IS DISTINCT FROM smoke.instance_id::text OR receipt.route->'Caller'->>'capture_instance_id' IS DISTINCT FROM q.reserved_instance_id::text OR
    receipt.route->'Target'->>'request_id' IS DISTINCT FROM target.id::text OR receipt.route->'Target'->>'attempt' IS DISTINCT FROM target.attempt::text OR
    receipt.route->'Target'->>'capture_instance_id' IS DISTINCT FROM target.reserved_instance_id::text OR
    receipt.route->'Target'->>'deployment_id' IS DISTINCT FROM target.deployment_id::text OR
    receipt.route->'Target'->>'instance_id' IS DISTINCT FROM (SELECT instance_id::text FROM environment_qualification_restore_receipts WHERE request_id=target.id AND attempt=target.attempt)
    THEN RETURN NULL; END IF;
   deadline:=least(deadline,(receipt.route->>'Deadline')::timestamptz,receipt.recorded_at+p.max_age_seconds*interval '1 second');
  END LOOP;
  until_at:=least(until_at,deadline);
 END LOOP;
 IF count_members=0 OR until_at<=clock_timestamp() THEN RETURN NULL; END IF;
 RETURN until_at;
END $$;
-- +goose StatementEnd

-- +goose Down
-- Preserve credential receipt history during a binary downgrade.
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM environment_qualification_postgres_receipts) THEN
  RAISE EXCEPTION 'cannot discard candidate PostgreSQL receipt history'; END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION environment_binding_catalog_supported(target_app uuid,target_scope text) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT NOT EXISTS(SELECT 1 FROM managed_postgres_bindings WHERE app_id=target_app AND scope=target_scope AND state<>'deleted')
 AND NOT EXISTS(SELECT 1 FROM object_storage_s3_credentials WHERE managed_app_id=target_app AND managed_scope=target_scope AND status='active')
 AND NOT EXISTS(SELECT 1 FROM outbound_app_bindings WHERE app_id=target_app)
 AND NOT EXISTS(SELECT 1 FROM outbound_integration_apps WHERE app_id=target_app);
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION environment_binding_qualification_deadline(target_graph uuid) RETURNS timestamptz LANGUAGE plpgsql AS $$
DECLARE g environment_workload_graphs%ROWTYPE; s environment_git_sources%ROWTYPE; e project_environments%ROWTYPE;
 q environment_workload_qualification_requests%ROWTYPE; p app_binding_release_policies%ROWTYPE;
 smoke environment_qualification_smoke_receipts%ROWTYPE; edge record; target environment_workload_qualification_requests%ROWTYPE;
 receipt environment_qualification_service_receipts%ROWTYPE; until_at timestamptz; deadline timestamptz; count_members integer:=0;
BEGIN
 SELECT * INTO g FROM environment_workload_graphs WHERE id=target_graph;
 SELECT * INTO s FROM active_environment_git_sources WHERE id=g.source_id;
 SELECT * INTO e FROM project_environments WHERE id=g.environment_id;
 IF g.id IS NULL OR s.id IS NULL OR s.mode<>'enforce' OR s.suspended OR g.generation<>s.generation OR
 g.intent_version<>s.intent_version OR g.revision_id<>s.approved_revision_id OR g.phase<>'prepared' THEN RETURN NULL; END IF;
 IF EXISTS(SELECT 1 FROM jsonb_array_elements(g.members) m WHERE coalesce(m->>'candidate_deployment_id','')='') THEN RETURN NULL; END IF;
 IF (SELECT count(*) FROM jsonb_array_elements(g.members))<>(SELECT count(*) FROM environment_workload_qualification_requests WHERE graph_id=g.id) THEN RETURN NULL; END IF;
 FOR q IN SELECT * FROM environment_workload_qualification_requests WHERE graph_id=g.id ORDER BY app_id LOOP
  count_members:=count_members+1;
  SELECT * INTO p FROM app_binding_release_policies WHERE app_id=q.app_id AND scope=e.slug;
  SELECT * INTO smoke FROM environment_qualification_smoke_receipts WHERE request_id=q.id AND attempt=q.attempt;
  IF q.binding_context IS NULL OR p.app_id IS NULL OR p.mode<>'enforce' OR p.require_application_ack OR p.object_storage_requirements<>'{}'::jsonb OR
   q.binding_context->'policy'->>'revision' IS DISTINCT FROM p.revision::text OR q.binding_context->'policy'->>'mode' IS DISTINCT FROM p.mode OR
   q.binding_context->'policy'->>'scope' IS DISTINCT FROM e.slug OR q.binding_context->'policy'->>'app_id' IS DISTINCT FROM q.app_id::text OR
   coalesce(q.binding_context->'policy'->'queue_requirements','{}'::jsonb) IS DISTINCT FROM p.queue_requirements OR
   coalesce(q.binding_context->'policy'->'object_storage_requirements','{}'::jsonb) IS DISTINCT FROM p.object_storage_requirements OR
   NOT environment_binding_catalog_supported(q.app_id,e.slug) OR NOT environment_workload_qualification_evidence_current(q.id) OR
   q.binding_context->'artifact_inputs' IS DISTINCT FROM (SELECT environment_binding_artifact_inputs(d) FROM deployments d WHERE d.id=q.deployment_id) OR
   q.binding_context->>'service_revision' IS DISTINCT FROM (SELECT epoch::text||':'||service_revision::text FROM app_binding_promotion_revisions WHERE app_id=q.app_id) OR
   EXISTS(SELECT 1 FROM queue_bindings b WHERE b.app_id=q.app_id AND coalesce(nullif(b.deployment_scope,''),'default') IN (e.slug,'default') AND b.retired_at IS NULL AND NOT EXISTS(SELECT 1 FROM jsonb_each(coalesce(q.frozen_inputs->'queue_bindings','{}'::jsonb)) f WHERE f.value->>'binding_id'=b.id::text)) OR
   q.execution_mode NOT IN ('request','service','worker') OR q.frozen_inputs->>'workload_class' NOT IN ('http','worker') OR
   smoke.request_id IS NULL OR smoke.recorded_at>clock_timestamp() OR q.lease_until IS NULL THEN RETURN NULL; END IF;
  IF NOT EXISTS(SELECT 1 FROM environment_qualification_snapshot_receipts capture
   JOIN environment_qualification_executions original ON original.instance_id=capture.instance_id
   JOIN environment_qualification_restore_receipts restored ON restored.request_id=q.id AND restored.attempt=q.attempt AND restored.capture_instance_id=capture.instance_id
   JOIN environment_qualification_executions runtime ON runtime.instance_id=restored.instance_id
   JOIN environment_qualification_framework_ready_receipts ready ON ready.request_id=q.id AND ready.attempt=q.attempt AND ready.instance_id=restored.instance_id
   JOIN environment_qualification_config_receipts source_config ON source_config.instance_id=capture.instance_id
   JOIN environment_qualification_config_receipts restore_config ON restore_config.instance_id=restored.instance_id
   WHERE capture.instance_id=q.reserved_instance_id AND restored.instance_id=smoke.instance_id AND smoke.graph_id=g.id AND smoke.capture_instance_id=q.reserved_instance_id
    AND original.dispatch_started AND runtime.dispatch_started AND original.retired_at IS NOT NULL AND runtime.retired_at IS NOT NULL
    AND original.retirement->>'kind'='native_retired' AND runtime.retirement->>'kind'='native_retired'
    AND environment_runtime_inputs_fresh(q.app_id,e.slug,(capture.inputs->>'boundary')::timestamptz,capture.inputs->'variables',capture.inputs->'secret_versions',(capture.inputs->>'all_secrets')::boolean,capture.inputs->'secret_refs',coalesce(capture.inputs->'sidecar_secret_versions','{}'::jsonb))
    AND environment_runtime_inputs_fresh(q.app_id,e.slug,(restored.runtime_inputs->>'boundary')::timestamptz,restored.runtime_inputs->'variables',restored.runtime_inputs->'secret_versions',(restored.runtime_inputs->>'all_secrets')::boolean,restored.runtime_inputs->'secret_refs',coalesce(restored.runtime_inputs->'sidecar_secret_versions','{}'::jsonb))) THEN RETURN NULL; END IF;
  IF coalesce(q.frozen_inputs->'service_bindings','{}'::jsonb)<>'{}'::jsonb AND
   coalesce(nullif(q.frozen_inputs->'runtime'->>'service_binding_transport',''),q.frozen_inputs->'baseline'->>'service_binding_transport','http')<>'https' THEN RETURN NULL; END IF;
  IF (SELECT count(*) FROM jsonb_each(coalesce(q.frozen_inputs->'queue_bindings','{}'::jsonb)))<>(SELECT count(*) FROM jsonb_each(p.queue_requirements)) OR
   EXISTS(SELECT 1 FROM jsonb_each(coalesce(q.frozen_inputs->'queue_bindings','{}'::jsonb)) b
    WHERE q.execution_mode<>'worker' OR b.value->'contract'->>'mode'<>'push' OR b.value->'contract'->>'enabled' IS DISTINCT FROM 'true' OR
     p.queue_requirements->>b.key IS DISTINCT FROM 'handler_completion' OR NOT coalesce(q.frozen_inputs->'queue_smoke','{}'::jsonb) ? b.key) THEN RETURN NULL; END IF;
  deadline:=least(q.lease_until,smoke.recorded_at+p.max_age_seconds*interval '1 second');
  FOR edge IN SELECT key,value FROM jsonb_each(coalesce(q.frozen_inputs->'service_bindings','{}'::jsonb)) LOOP
   SELECT * INTO target FROM environment_workload_qualification_requests WHERE graph_id=g.id AND app_id=(edge.value->>'target_app_id')::uuid;
   SELECT * INTO receipt FROM environment_qualification_service_receipts WHERE request_id=q.id AND attempt=q.attempt AND binding=edge.key;
   IF target.id IS NULL OR receipt.request_id IS NULL OR receipt.recorded_at>smoke.recorded_at OR receipt.route->>'RequireHTTPS' IS DISTINCT FROM 'true' OR
    receipt.route->'Caller'->>'request_id' IS DISTINCT FROM q.id::text OR receipt.route->'Caller'->>'attempt' IS DISTINCT FROM q.attempt::text OR
    receipt.route->'Caller'->>'instance_id' IS DISTINCT FROM smoke.instance_id::text OR receipt.route->'Caller'->>'capture_instance_id' IS DISTINCT FROM q.reserved_instance_id::text OR
    receipt.route->'Target'->>'request_id' IS DISTINCT FROM target.id::text OR receipt.route->'Target'->>'attempt' IS DISTINCT FROM target.attempt::text OR
    receipt.route->'Target'->>'capture_instance_id' IS DISTINCT FROM target.reserved_instance_id::text OR
    receipt.route->'Target'->>'deployment_id' IS DISTINCT FROM target.deployment_id::text OR
    receipt.route->'Target'->>'instance_id' IS DISTINCT FROM (SELECT instance_id::text FROM environment_qualification_restore_receipts WHERE request_id=target.id AND attempt=target.attempt)
    THEN RETURN NULL; END IF;
   deadline:=least(deadline,(receipt.route->>'Deadline')::timestamptz,receipt.recorded_at+p.max_age_seconds*interval '1 second');
  END LOOP;
  until_at:=least(until_at,deadline);
 END LOOP;
 IF count_members=0 OR until_at<=clock_timestamp() THEN RETURN NULL; END IF;
 RETURN until_at;
END $$;
-- +goose StatementEnd
DROP FUNCTION environment_binding_postgres_deadline(environment_workload_qualification_requests,text,bigint,uuid);
DROP TABLE environment_qualification_postgres_receipts;
DROP FUNCTION guard_environment_qualification_postgres_receipt();
