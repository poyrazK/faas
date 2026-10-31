-- filename: 20261019161558609_candidate_outbound_binding_qualification.sql
-- ADR-20261019152416: outboundd's own probe receipts admit outbound
-- integration bindings into candidate binding qualification.
-- migration-compat: expand

-- +goose Up
SET LOCAL lock_timeout = '5s';
-- SHA-256 over the facts a candidate probe exercises: the integration, the
-- app binding, the plan, the sealed credential, the key pool and the probe
-- policy. NULL unless the binding is active and probe-ready. Timestamps are
-- hashed as epoch microseconds so the session time zone cannot change it.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION environment_binding_outbound_revision(target_integration uuid,target_app uuid) RETURNS text LANGUAGE sql STABLE AS $$
 SELECT encode(sha256(convert_to(jsonb_build_array(to_jsonb(i)-'updated_at'-'created_at',to_jsonb(b)-'created_at',a.plan,
  encode(sha256(c.authorization_sealed),'hex'),(extract(epoch FROM c.updated_at)*1000000)::bigint,
  (SELECT coalesce(jsonb_agg(jsonb_build_array(k.id,k.label,encode(sha256(k.authorization_sealed),'hex'),(extract(epoch FROM k.created_at)*1000000)::bigint) ORDER BY k.id),'[]'::jsonb)
   FROM outbound_integration_credential_keys k WHERE k.integration_id=i.id AND k.account_id=i.account_id),
  jsonb_build_array(p.method,p.path,p.expected_status))::text,'UTF8')),'hex')
 FROM outbound_app_bindings b
 JOIN outbound_integrations i ON i.id=b.integration_id AND i.account_id=b.account_id
 JOIN accounts a ON a.id=b.account_id
 JOIN apps app ON app.id=b.app_id AND app.account_id=b.account_id
 JOIN outbound_integration_credentials c ON c.integration_id=i.id AND c.account_id=i.account_id
 JOIN outbound_integration_probe_policies p ON p.integration_id=i.id AND p.account_id=i.account_id
 WHERE b.integration_id=target_integration AND b.app_id=target_app AND i.credential_source='customer_sealed'
  AND i.enabled AND i.provider_auth_mode='managed' AND cardinality(i.allowed_methods)>0 AND cardinality(i.allowed_path_prefixes)>0;
$$;
-- +goose StatementEnd

-- One immutable receipt per request attempt, integration and binding
-- revision. outboundd writes it after its own probe returned the policy's
-- expected status; a changed revision needs a new probe.
CREATE TABLE IF NOT EXISTS environment_qualification_outbound_receipts (
 request_id uuid NOT NULL REFERENCES environment_workload_qualification_requests(id) ON DELETE CASCADE,
 attempt bigint NOT NULL CHECK(attempt>0),
 integration_id uuid NOT NULL,
 revision text NOT NULL CHECK(revision ~ '^[a-f0-9]{64}$'),
 method text NOT NULL CHECK(method IN ('GET','HEAD')),
 path text NOT NULL CHECK(length(path) BETWEEN 1 AND 512 AND path ~ '^/[^?#]*$'),
 status integer NOT NULL CHECK(status BETWEEN 200 AND 299),
 recorded_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(request_id,attempt,integration_id,revision)
);

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_environment_qualification_outbound_receipt() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP<>'INSERT' THEN
  IF TG_OP='DELETE' AND pg_trigger_depth()>1 THEN RETURN OLD; END IF;
  RAISE EXCEPTION 'candidate outbound receipts are immutable' USING ERRCODE='23514';
 END IF;
 IF NOT EXISTS(SELECT 1 FROM environment_workload_qualification_requests q
  JOIN outbound_integration_probe_policies p ON p.integration_id=NEW.integration_id
  WHERE q.id=NEW.request_id AND q.attempt=NEW.attempt AND q.binding_context IS NOT NULL AND q.lease_until>clock_timestamp()
   AND p.method=NEW.method AND p.path=NEW.path AND p.expected_status=NEW.status
   AND environment_binding_outbound_revision(NEW.integration_id,q.app_id)=NEW.revision) THEN
  RAISE EXCEPTION 'candidate outbound receipt requires the current qualification lease and binding revision' USING ERRCODE='23514',CONSTRAINT='binding_release_changed';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS guard_environment_qualification_outbound_receipt ON environment_qualification_outbound_receipts;
CREATE TRIGGER guard_environment_qualification_outbound_receipt BEFORE INSERT OR UPDATE OR DELETE ON environment_qualification_outbound_receipts
 FOR EACH ROW EXECUTE FUNCTION guard_environment_qualification_outbound_receipt();

-- Returns 'infinity' without active outbound bindings, and NULL when an
-- active binding is not probe-ready or has no receipt for its current
-- revision. Inactive integrations are skipped, as binding verification does.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION environment_binding_outbound_deadline(q environment_workload_qualification_requests,max_age_seconds bigint) RETURNS timestamptz LANGUAGE plpgsql STABLE AS $$
DECLARE bound record; current_revision text; recorded timestamptz; until_at timestamptz:='infinity';
BEGIN
 FOR bound IN SELECT b.integration_id,i.id IS NOT NULL AS known,
   coalesce(i.enabled AND i.provider_auth_mode='managed' AND cardinality(i.allowed_methods)>0 AND cardinality(i.allowed_path_prefixes)>0,false) AS active
  FROM outbound_app_bindings b LEFT JOIN outbound_integrations i ON i.id=b.integration_id AND i.account_id=b.account_id
  WHERE b.app_id=q.app_id ORDER BY b.integration_id LOOP
  IF NOT bound.known THEN RETURN NULL; END IF;
  IF NOT bound.active THEN CONTINUE; END IF;
  current_revision:=environment_binding_outbound_revision(bound.integration_id,q.app_id);
  SELECT r.recorded_at INTO recorded FROM environment_qualification_outbound_receipts r
   WHERE r.request_id=q.id AND r.attempt=q.attempt AND r.integration_id=bound.integration_id AND r.revision=current_revision;
  IF current_revision IS NULL OR recorded IS NULL OR recorded>clock_timestamp() THEN RETURN NULL; END IF;
  until_at:=least(until_at,recorded+max_age_seconds*interval '1 second');
 END LOOP;
 RETURN until_at;
END $$;
-- +goose StatementEnd

-- Customer outbound bindings are now qualified by receipts in the deadline
-- below. Operator attachments have no probe policy and stay blocked.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION environment_binding_catalog_supported(target_app uuid,target_scope text) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT NOT EXISTS(SELECT 1 FROM outbound_integration_apps WHERE app_id=target_app);
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION environment_binding_qualification_deadline(target_graph uuid) RETURNS timestamptz LANGUAGE plpgsql AS $$
DECLARE g environment_workload_graphs%ROWTYPE; s environment_git_sources%ROWTYPE; e project_environments%ROWTYPE;
 q environment_workload_qualification_requests%ROWTYPE; p app_binding_release_policies%ROWTYPE;
 smoke environment_qualification_smoke_receipts%ROWTYPE; edge record; target environment_workload_qualification_requests%ROWTYPE;
 receipt environment_qualification_service_receipts%ROWTYPE; until_at timestamptz; deadline timestamptz; postgres_deadline timestamptz; storage_deadline timestamptz; outbound_deadline timestamptz; count_members integer:=0;
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
  IF q.binding_context IS NULL OR p.app_id IS NULL OR p.mode<>'enforce' OR p.require_application_ack OR
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
  storage_deadline:=environment_binding_object_storage_deadline(q,e.slug,p.object_storage_requirements,p.max_age_seconds,smoke.instance_id);
  IF storage_deadline IS NULL THEN RETURN NULL; END IF;
  deadline:=least(deadline,storage_deadline);
  outbound_deadline:=environment_binding_outbound_deadline(q,p.max_age_seconds);
  IF outbound_deadline IS NULL THEN RETURN NULL; END IF;
  deadline:=least(deadline,outbound_deadline);
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
-- Preserve probe receipt history during a binary downgrade.
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM environment_qualification_outbound_receipts) THEN
  RAISE EXCEPTION 'cannot discard candidate outbound receipt history'; END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION environment_binding_catalog_supported(target_app uuid,target_scope text) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT NOT EXISTS(SELECT 1 FROM outbound_app_bindings WHERE app_id=target_app)
 AND NOT EXISTS(SELECT 1 FROM outbound_integration_apps WHERE app_id=target_app);
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION environment_binding_qualification_deadline(target_graph uuid) RETURNS timestamptz LANGUAGE plpgsql AS $$
DECLARE g environment_workload_graphs%ROWTYPE; s environment_git_sources%ROWTYPE; e project_environments%ROWTYPE;
 q environment_workload_qualification_requests%ROWTYPE; p app_binding_release_policies%ROWTYPE;
 smoke environment_qualification_smoke_receipts%ROWTYPE; edge record; target environment_workload_qualification_requests%ROWTYPE;
 receipt environment_qualification_service_receipts%ROWTYPE; until_at timestamptz; deadline timestamptz; postgres_deadline timestamptz; storage_deadline timestamptz; count_members integer:=0;
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
  IF q.binding_context IS NULL OR p.app_id IS NULL OR p.mode<>'enforce' OR p.require_application_ack OR
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
  storage_deadline:=environment_binding_object_storage_deadline(q,e.slug,p.object_storage_requirements,p.max_age_seconds,smoke.instance_id);
  IF storage_deadline IS NULL THEN RETURN NULL; END IF;
  deadline:=least(deadline,storage_deadline);
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
DROP FUNCTION environment_binding_outbound_deadline(environment_workload_qualification_requests,bigint);
DROP TABLE environment_qualification_outbound_receipts;
DROP FUNCTION guard_environment_qualification_outbound_receipt();
DROP FUNCTION environment_binding_outbound_revision(uuid,uuid);
