-- ADR-1331: preserve original workload identities through scoped retirement.
-- +goose Up
-- This Up is transactional. Its final source-lifecycle trigger marks completion.
-- Ledger replay must preserve retained records and later guard refinements.
-- +goose StatementBegin
DO $workload_retirement_replay$ BEGIN
 IF EXISTS(SELECT 1 FROM pg_trigger WHERE tgrelid='environment_git_sources'::regclass
  AND tgname='zz_workload_retirement_source_lifecycle' AND NOT tgisinternal) THEN RETURN; END IF;
CREATE TABLE environment_workload_retirements (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 source_id uuid NOT NULL REFERENCES environment_git_sources(id) ON DELETE CASCADE,
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 project_id uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
 environment_id uuid NOT NULL REFERENCES project_environments(id) ON DELETE CASCADE,
 app_id uuid NOT NULL REFERENCES apps(id) ON DELETE RESTRICT,
 resource text NOT NULL CHECK(resource ~ '^workload/[a-z0-9][a-z0-9-]*$'),
 revision_id uuid NOT NULL REFERENCES environment_desired_revisions(id) ON DELETE RESTRICT,
 generation bigint NOT NULL CHECK(generation>0),
 plan_hash text NOT NULL CHECK(plan_hash ~ '^[a-f0-9]{64}$'),
 phase text NOT NULL DEFAULT 'planned' CHECK(phase IN ('planned','retiring','retired','recovering','recovered','cancelled')),
 deployment_ids uuid[] NOT NULL DEFAULT '{}' CHECK(array_position(deployment_ids,NULL) IS NULL),
 queue_binding_ids uuid[] NOT NULL DEFAULT '{}' CHECK(array_position(queue_binding_ids,NULL) IS NULL),
 job_id uuid REFERENCES jobs(id) ON DELETE RESTRICT,
 route_deployment_id uuid REFERENCES deployments(id) ON DELETE RESTRICT,
 route_generation bigint NOT NULL DEFAULT 0 CHECK(route_generation>=0),
 expected_gateways text[] NOT NULL DEFAULT '{}' CHECK(array_position(expected_gateways,NULL) IS NULL),
 acknowledged_gateways text[] NOT NULL DEFAULT '{}' CHECK(array_position(acknowledged_gateways,NULL) IS NULL AND acknowledged_gateways <@ expected_gateways),
 requested_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 retired_at timestamptz,
 recovery_revision_id uuid REFERENCES environment_desired_revisions(id) ON DELETE RESTRICT,
 recovery_generation bigint NOT NULL DEFAULT 0 CHECK(recovery_generation>=0),
 recovered_at timestamptz,
 UNIQUE(source_id,resource,generation),
 CHECK(route_deployment_id IS NULL OR route_deployment_id=ANY(deployment_ids)),
 CHECK((phase IN ('planned','retiring','cancelled'))=(retired_at IS NULL)),
 CHECK(retired_at IS NULL OR retired_at>=requested_at),
 CHECK((phase IN ('recovering','recovered'))=(recovery_revision_id IS NOT NULL)),
 CHECK((recovery_revision_id IS NULL)=(recovery_generation=0)),
 CHECK(recovery_generation=0 OR recovery_generation>generation),
 CHECK((phase='recovered')=(recovered_at IS NOT NULL)),
 CHECK(recovered_at IS NULL OR recovered_at>=retired_at)
);
CREATE UNIQUE INDEX environment_workload_retirement_active_scope
 ON environment_workload_retirements(app_id,environment_id) WHERE phase NOT IN ('recovered','cancelled');
CREATE INDEX environment_workload_retirements_source ON environment_workload_retirements(source_id,requested_at,id);

-- The durable fence survives source suspension, detach and controller lease
-- expiry. Admission uses physical scoped identity, never a caller's Git ref.
CREATE FUNCTION environment_workload_admission_retired(application uuid, deployment_scope text) RETURNS boolean
 LANGUAGE sql STABLE AS $$
 SELECT EXISTS(SELECT 1 FROM environment_workload_retirements r
  JOIN project_environments e ON e.id=r.environment_id
  WHERE r.app_id=application AND e.slug=CASE WHEN coalesce(deployment_scope,'') IN ('','default') THEN 'production' ELSE deployment_scope END AND r.phase IN ('retiring','retired','recovering'))
$$;

CREATE FUNCTION guard_environment_workload_retirement() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE src environment_git_sources%ROWTYPE; rev environment_desired_revisions%ROWTYPE; token text; original_app uuid;
BEGIN
 IF TG_OP='DELETE' THEN
  IF EXISTS(SELECT 1 FROM accounts WHERE id=OLD.account_id AND status<>'deleted_pending') THEN
   RAISE EXCEPTION 'workload retirement history must be retained' USING ERRCODE='23514';
  END IF;
  RETURN OLD;
 END IF;
 SELECT * INTO src FROM active_environment_git_sources WHERE id=NEW.source_id FOR UPDATE;
 token:=nullif(current_setting('gregale.gitops_lease',true),'');
 IF src.id IS NULL OR src.mode<>'enforce' OR src.suspended OR token IS NULL OR
  ROW(src.account_id,src.project_id,src.environment_id) IS DISTINCT FROM ROW(NEW.account_id,NEW.project_id,NEW.environment_id) OR
  NOT EXISTS(SELECT 1 FROM environment_gitops_jobs j WHERE j.source_id=src.id AND j.desired_generation=src.generation
   AND j.claimed_generation=src.generation AND j.lease_token=token AND j.lease_until>clock_timestamp()) THEN
  RAISE EXCEPTION 'workload retirement requires its current scoped lease' USING ERRCODE='23514';
 END IF;
 SELECT * INTO rev FROM environment_desired_revisions WHERE id=src.approved_revision_id AND source_id=src.id;
 SELECT app_id INTO original_app FROM environment_gitops_resources WHERE source_id=src.id AND logical_name=NEW.resource;
 IF rev.id IS NULL OR original_app IS DISTINCT FROM NEW.app_id OR NOT EXISTS(
  SELECT 1 FROM apps WHERE id=NEW.app_id AND account_id=src.account_id AND project_id=src.project_id AND status<>'deleted') THEN
  RAISE EXCEPTION 'workload retirement lost its original identity' USING ERRCODE='23514';
 END IF;
 IF TG_OP='INSERT' THEN
  IF NOT src.prune OR jsonb_typeof(rev.definition->'workloads') IS DISTINCT FROM 'object' OR
   rev.definition->>'workload_pruning_policy' IS DISTINCT FROM 'retain' OR
   rev.definition->'workloads' ? substring(NEW.resource from 10) OR NEW.revision_id<>rev.id OR NEW.generation<>src.generation OR
   NEW.route_deployment_id IS DISTINCT FROM (SELECT d.id FROM deployments d JOIN project_environments e ON e.id=src.environment_id
    WHERE d.app_id=NEW.app_id AND d.scope=e.slug AND d.environment_workload_runtime->>'source_id'=src.id::text
     AND NOT d.environment_workload_held
     AND coalesce(d.environment_workload_runtime->'runtime'->>'execution_mode',d.environment_workload_runtime->'baseline'->>'execution_mode','request')<>'job'
    ORDER BY d.id LIMIT 1) OR
   NEW.phase<>'planned' OR
   NEW.deployment_ids IS DISTINCT FROM ARRAY(SELECT d.id FROM deployments d JOIN project_environments e ON e.id=src.environment_id
    WHERE d.app_id=NEW.app_id AND d.scope=e.slug AND d.environment_workload_runtime->>'source_id'=src.id::text ORDER BY d.id) OR
   NEW.queue_binding_ids IS DISTINCT FROM ARRAY(SELECT q.binding_id FROM environment_gitops_queue_bindings q
    WHERE q.source_id=src.id AND q.resource=NEW.resource AND q.binding_id IS NOT NULL ORDER BY q.binding_id) OR
   NEW.job_id IS DISTINCT FROM (SELECT w.job_id FROM app_environment_workload_intents w WHERE w.app_id=NEW.app_id AND w.environment_id=src.environment_id) OR
   NEW.route_generation<>0 OR cardinality(NEW.expected_gateways)<>0 OR cardinality(NEW.acknowledged_gateways)<>0 OR
   NOT EXISTS(SELECT 1 FROM environment_managed_fields f WHERE f.source_id=src.id AND f.manager_id=src.id::text
    AND f.resource=NEW.resource AND f.field_path='presence') OR
   EXISTS(SELECT 1 FROM environment_management_overrides o WHERE o.environment_id=src.environment_id AND o.resource=NEW.resource AND o.expires_at>clock_timestamp()) THEN
   RAISE EXCEPTION 'workload retirement has no reviewed retain authority' USING ERRCODE='23514';
  END IF;
 ELSE
  IF ROW(NEW.id,NEW.source_id,NEW.account_id,NEW.project_id,NEW.environment_id,NEW.app_id,NEW.resource,NEW.revision_id,
   NEW.generation,NEW.plan_hash,NEW.deployment_ids,NEW.queue_binding_ids,NEW.job_id,NEW.route_deployment_id,NEW.requested_at)
   IS DISTINCT FROM ROW(OLD.id,OLD.source_id,OLD.account_id,OLD.project_id,OLD.environment_id,OLD.app_id,OLD.resource,OLD.revision_id,
   OLD.generation,OLD.plan_hash,OLD.deployment_ids,OLD.queue_binding_ids,OLD.job_id,OLD.route_deployment_id,OLD.requested_at) OR
   OLD.retired_at IS NOT NULL AND NEW.retired_at IS DISTINCT FROM OLD.retired_at OR
   OLD.recovered_at IS NOT NULL AND NEW.recovered_at IS DISTINCT FROM OLD.recovered_at THEN
   RAISE EXCEPTION 'original workload retirement records are immutable' USING ERRCODE='23514';
  END IF;
  IF NEW.phase IS DISTINCT FROM OLD.phase AND NOT (
   OLD.phase='planned' AND NEW.phase IN ('retiring','cancelled') OR
   OLD.phase='retiring' AND NEW.phase='retired' OR OLD.phase='retired' AND NEW.phase='recovering' OR
   OLD.phase='recovering' AND NEW.phase IN ('recovered','retired')) THEN
   RAISE EXCEPTION 'invalid workload retirement transition' USING ERRCODE='23514';
  END IF;
  IF NEW.phase='recovering' AND (OLD.phase IS DISTINCT FROM 'recovering' OR
   ROW(NEW.recovery_revision_id,NEW.recovery_generation) IS DISTINCT FROM ROW(OLD.recovery_revision_id,OLD.recovery_generation)) AND (
   rev.definition->'workload_recoveries'->>substring(NEW.resource from 10) IS DISTINCT FROM NEW.app_id::text OR
   NOT (rev.definition->'workloads' ? substring(NEW.resource from 10)) OR NEW.recovery_revision_id<>rev.id OR NEW.recovery_generation<>src.generation) THEN
   RAISE EXCEPTION 'workload recovery must review the original retained app identity' USING ERRCODE='23514';
  END IF;
  IF NEW.phase='retiring' AND OLD.phase='planned' AND (
   NOT environment_workload_retirement_cutover_authorized(NEW) OR NOT src.prune OR
   rev.definition->>'workload_pruning_policy' IS DISTINCT FROM 'retain' OR rev.definition->'workloads' ? substring(NEW.resource from 10) OR
   NEW.generation<>src.generation OR NEW.revision_id<>rev.id OR
   NOT EXISTS(SELECT 1 FROM environment_managed_fields f WHERE f.source_id=src.id AND f.manager_id=src.id::text AND f.resource=NEW.resource AND f.field_path='presence') OR
   EXISTS(SELECT 1 FROM environment_managed_fields f WHERE f.environment_id=src.environment_id AND f.resource=NEW.resource AND f.manager_id<>src.id::text) OR
   EXISTS(SELECT 1 FROM environment_external_field_owners f WHERE f.environment_id=src.environment_id AND f.resource IN (NEW.resource,'app/'||NEW.app_id::text)) OR
   EXISTS(SELECT 1 FROM deployments d JOIN project_environments e ON e.id=src.environment_id WHERE d.app_id=NEW.app_id AND d.scope=e.slug
    AND d.status='live' AND d.environment_workload_runtime->>'source_id' IS DISTINCT FROM src.id::text) OR
   NEW.deployment_ids IS DISTINCT FROM ARRAY(SELECT d.id FROM deployments d JOIN project_environments e ON e.id=src.environment_id
    WHERE d.app_id=NEW.app_id AND d.scope=e.slug AND d.environment_workload_runtime->>'source_id'=src.id::text ORDER BY d.id) OR
   NEW.queue_binding_ids IS DISTINCT FROM ARRAY(SELECT q.binding_id FROM environment_gitops_queue_bindings q
    WHERE q.source_id=src.id AND q.resource=NEW.resource AND q.binding_id IS NOT NULL ORDER BY q.binding_id) OR
   EXISTS(SELECT 1 FROM queue_bindings b WHERE b.app_id=NEW.app_id AND b.environment_id=src.environment_id
    AND NOT (b.id=ANY(NEW.queue_binding_ids))) OR
   EXISTS(SELECT 1 FROM environment_management_overrides o WHERE o.environment_id=src.environment_id AND o.resource=NEW.resource AND o.expires_at>clock_timestamp())) THEN
   RAISE EXCEPTION 'retirement requires the current qualified cutover' USING ERRCODE='23514';
  END IF;
  IF NEW.phase='cancelled' AND OLD.phase='planned' AND NEW.generation>=src.generation THEN
   RAISE EXCEPTION 'current retirement reservation cannot be cancelled' USING ERRCODE='23514';
  END IF;
  IF NEW.phase='retired' AND OLD.phase='retiring' AND NOT environment_workload_retirement_drained(NEW) THEN
   RAISE EXCEPTION 'retirement requires route withdrawal and original execution drain' USING ERRCODE='23514';
  END IF;
  IF NEW.phase='retired' AND OLD.phase='recovering' AND (src.generation<=OLD.recovery_generation OR rev.definition->'workloads' ? substring(NEW.resource from 10)) THEN
   RAISE EXCEPTION 'only a superseding removal can abandon recovery' USING ERRCODE='23514';
  END IF;
  IF NEW.route_generation IS DISTINCT FROM OLD.route_generation OR NEW.expected_gateways IS DISTINCT FROM OLD.expected_gateways THEN
   IF NEW.route_generation<=OLD.route_generation OR cardinality(NEW.acknowledged_gateways)<>0 OR
    NEW.expected_gateways IS DISTINCT FROM environment_workload_retirement_gateway_roster() THEN
    RAISE EXCEPTION 'route withdrawal requires a fresh generation and current gateway roster' USING ERRCODE='23514';
   END IF;
  ELSIF NOT OLD.acknowledged_gateways <@ NEW.acknowledged_gateways THEN
   RAISE EXCEPTION 'withdrawal acknowledgements cannot be removed' USING ERRCODE='23514';
  END IF;
  IF NEW.phase='recovered' AND OLD.phase='recovering' AND NOT EXISTS(
   SELECT 1 FROM deployments d WHERE d.app_id=NEW.app_id AND d.scope=(SELECT slug FROM project_environments WHERE id=NEW.environment_id)
    AND d.environment_workload_runtime->>'source_id'=src.id::text AND d.environment_workload_runtime->>'revision_id'=rev.id::text
    AND d.environment_workload_runtime->>'generation'=src.generation::text AND environment_workload_activation_authorized(d)) THEN
   RAISE EXCEPTION 'workload recovery requires fresh qualified activation' USING ERRCODE='23514';
  END IF;
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER guard_environment_workload_retirement BEFORE INSERT OR UPDATE OR DELETE
 ON environment_workload_retirements FOR EACH ROW EXECUTE FUNCTION guard_environment_workload_retirement();


CREATE FUNCTION environment_workload_retirement_gateway_roster() RETURNS text[] LANGUAGE sql STABLE AS $$
 SELECT ARRAY(SELECT name FROM compute_nodes WHERE active AND role IN ('compute-only','compute-node')
  AND nullif(btrim(gateway_target_url),'') IS NOT NULL ORDER BY name)
$$;
CREATE FUNCTION environment_workload_retirement_routes_withdrawn(r environment_workload_retirements) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT (r).route_deployment_id IS NULL OR ((r).route_generation>0 AND cardinality((r).expected_gateways)>0
  AND (r).expected_gateways=environment_workload_retirement_gateway_roster()
  AND (r).expected_gateways <@ (r).acknowledged_gateways)
$$;
CREATE FUNCTION environment_workload_retirement_drained(r environment_workload_retirements) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT environment_workload_retirement_routes_withdrawn(r)
 AND ((r).job_id IS NULL OR EXISTS(SELECT 1 FROM jobs j WHERE j.id=(r).job_id AND j.status IN ('paused','deleted')))
 AND NOT EXISTS(SELECT 1 FROM unnest((r).queue_binding_ids) original(id) WHERE NOT EXISTS(
  SELECT 1 FROM queue_bindings b WHERE b.id=original.id AND b.app_id=(r).app_id AND b.environment_id=(r).environment_id AND NOT b.enabled AND b.retired_at IS NOT NULL))
 AND NOT EXISTS(SELECT 1 FROM triggers consumer WHERE consumer.queue_binding_id=ANY((r).queue_binding_ids) AND consumer.enabled)

 AND NOT EXISTS(SELECT 1 FROM instances i WHERE (i.deployment_id=ANY((r).deployment_ids) OR i.job_id=(r).job_id) AND i.state NOT IN ('parked','stopped','failed'))
 AND NOT EXISTS(SELECT 1 FROM deployments d WHERE d.id=ANY((r).deployment_ids) AND d.status NOT IN ('superseded','cancelled','failed'))
 AND NOT EXISTS(SELECT 1 FROM environment_qualification_executions x
  WHERE (x.frame->>'deployment_id')::uuid=ANY((r).deployment_ids) AND x.retired_at IS NULL)
 AND NOT EXISTS(SELECT 1 FROM invocations i WHERE i.app_id=(r).app_id AND (i.environment_id=(r).environment_id OR CASE WHEN coalesce(i.deployment_scope,'') IN ('','default') THEN 'production' ELSE i.deployment_scope END=(SELECT slug FROM project_environments WHERE id=(r).environment_id)) AND i.state='dispatching')
 AND NOT EXISTS(SELECT 1 FROM trigger_records receipt JOIN triggers consumer ON consumer.id=receipt.trigger_id
  WHERE consumer.queue_binding_id=ANY((r).queue_binding_ids) AND receipt.state='claimed' AND receipt.claim_expires_at>clock_timestamp())
 AND NOT EXISTS(SELECT 1 FROM job_tasks t JOIN job_runs j ON j.id=t.run_id WHERE j.job_id=(r).job_id AND t.status='claimed')
$$;
-- The activation token is set only inside the qualified graph publication
-- transaction. Empty membership has no replacement workload to qualify.
CREATE FUNCTION environment_workload_retirement_cutover_authorized(r environment_workload_retirements) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT nullif(current_setting('gregale.gitops_activation',true),'')=nullif(current_setting('gregale.gitops_lease',true),'')
 AND EXISTS(SELECT 1 FROM active_environment_git_sources s JOIN environment_desired_revisions rev ON rev.id=s.approved_revision_id
  JOIN environment_gitops_jobs j ON j.source_id=s.id
  WHERE s.id=(r).source_id AND s.mode='enforce' AND NOT s.suspended AND s.generation=(r).generation AND rev.id=(r).revision_id
   AND j.desired_generation=s.generation AND j.claimed_generation=s.generation AND j.lease_token=current_setting('gregale.gitops_lease',true)
   AND j.lease_until>clock_timestamp() AND (rev.definition->'workloads'='{}'::jsonb OR EXISTS(
    SELECT 1 FROM environment_workload_graphs g WHERE g.source_id=s.id AND g.revision_id=rev.id AND g.generation=s.generation
     AND g.intent_version=s.intent_version AND g.phase='prepared' AND environment_workload_graph_serving_supported(g.id)
     AND NOT EXISTS(SELECT 1 FROM jsonb_array_elements(g.members) m WHERE NOT (
      CASE WHEN coalesce(m->>'candidate_deployment_id','')<>'' THEN EXISTS(
       SELECT 1 FROM deployments d WHERE d.id=(m->>'candidate_deployment_id')::uuid AND environment_workload_activation_authorized(d))
      ELSE EXISTS(SELECT 1 FROM project_release_sets rs JOIN project_release_members rm ON rm.release_id=rs.id
       JOIN deployments d ON d.id=rm.deployment_id JOIN project_environments e ON e.id=s.environment_id
       WHERE rs.project_id=s.project_id AND rs.environment_slug=e.slug AND rs.active AND rm.app_id=(m->>'app_id')::uuid
        AND m->'retained_deployments' ? d.id::text AND d.status='live' AND NOT d.environment_workload_held) END)))))
$$;
-- No public promotion, revision pin, cold wake, queue claim or manual Job run
-- may reopen a retired physical app/environment lane. Qualification executions
-- remain governed by their existing exact attempt guard during recovery.
CREATE FUNCTION guard_retired_workload_execution() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE application uuid; environment uuid; scope text; linked_job uuid; r environment_workload_retirements%ROWTYPE; d deployments%ROWTYPE;
BEGIN
 IF TG_TABLE_NAME='instances' THEN
  IF TG_OP='UPDATE' AND (NEW.state IS NOT DISTINCT FROM OLD.state OR NEW.state IN ('draining','snapshotting','parked','stopped','failed','evicting_account_deleting')) THEN RETURN NEW; END IF;
  SELECT * INTO d FROM deployments WHERE id=NEW.deployment_id;
  application:=NEW.app_id;scope:=d.scope;
 ELSIF TG_TABLE_NAME='deployments' THEN
  IF NEW.status<>'live' OR TG_OP='UPDATE' AND OLD.status='live' THEN RETURN NEW; END IF;
  application:=NEW.app_id;scope:=NEW.scope;
 ELSIF TG_TABLE_NAME='invocations' THEN
  IF TG_OP='UPDATE' AND (NEW.state<>'dispatching' OR OLD.state='dispatching') THEN RETURN NEW; END IF;
  application:=NEW.app_id;environment:=NEW.environment_id;scope:=NEW.deployment_scope;
 ELSIF TG_TABLE_NAME='trigger_records' THEN
  IF NEW.state<>'claimed' THEN RETURN NEW; END IF;
  IF TG_OP='UPDATE' THEN IF OLD.state='claimed' AND OLD.claim_generation=NEW.claim_generation THEN RETURN NEW; END IF; END IF;
  SELECT app_id,queue_binding_scope INTO application,scope FROM triggers WHERE id=NEW.trigger_id;
 ELSIF TG_TABLE_NAME='job_runs' THEN linked_job:=NEW.job_id;
 ELSIF TG_TABLE_NAME='job_tasks' THEN
  IF NEW.status<>'claimed' OR TG_OP='UPDATE' AND OLD.status='claimed' THEN RETURN NEW; END IF;
  SELECT job_id INTO linked_job FROM job_runs WHERE id=NEW.run_id;
 END IF;
 IF coalesce(scope,'') IN ('','default') THEN scope:='production'; END IF;
 IF application IS NOT NULL THEN
  PERFORM id FROM apps WHERE id=application FOR SHARE;
 ELSIF linked_job IS NOT NULL THEN
  PERFORM a.id FROM apps a JOIN environment_workload_retirements x ON x.app_id=a.id WHERE x.job_id=linked_job FOR SHARE OF a;
 END IF;
 SELECT * INTO r FROM environment_workload_retirements x WHERE x.phase IN ('retiring','retired','recovering') AND
  (linked_job IS NOT NULL AND x.job_id=linked_job OR application=x.app_id AND
   (environment=x.environment_id OR scope=(SELECT slug FROM project_environments WHERE id=x.environment_id)));
 IF r.id IS NULL THEN RETURN NEW; END IF;
 -- Serialize dispatch with the app lock held through cutover. Recheck after
 -- obtaining the lock to avoid admitting work from an earlier snapshot.
 PERFORM id FROM apps WHERE id=r.app_id FOR SHARE;
 IF NOT EXISTS(SELECT 1 FROM environment_workload_retirements WHERE id=r.id AND phase IN ('retiring','retired','recovering')) THEN RETURN NEW; END IF;
 IF r.phase='recovering' THEN
  IF TG_TABLE_NAME='deployments' THEN
   IF NEW.id<>ALL(r.deployment_ids) AND environment_workload_activation_authorized(NEW) THEN RETURN NEW; END IF;
  END IF;
  IF TG_TABLE_NAME='instances' AND d.environment_workload_held AND d.environment_workload_runtime->>'revision_id'=r.recovery_revision_id::text
   AND d.environment_workload_runtime->>'generation'=r.recovery_generation::text THEN RETURN NEW; END IF;
 END IF;
 RAISE EXCEPTION 'workload execution is retired in this environment' USING ERRCODE='23514',CONSTRAINT='environment_workload_retired';
END $$;
CREATE TRIGGER zz_retired_workload_execution BEFORE INSERT OR UPDATE ON instances FOR EACH ROW EXECUTE FUNCTION guard_retired_workload_execution();
CREATE TRIGGER zz_retired_workload_execution BEFORE INSERT OR UPDATE ON deployments FOR EACH ROW EXECUTE FUNCTION guard_retired_workload_execution();
CREATE TRIGGER zz_retired_workload_execution BEFORE INSERT OR UPDATE ON invocations FOR EACH ROW EXECUTE FUNCTION guard_retired_workload_execution();
CREATE TRIGGER zz_retired_workload_execution BEFORE INSERT OR UPDATE ON trigger_records FOR EACH ROW EXECUTE FUNCTION guard_retired_workload_execution();
CREATE TRIGGER zz_retired_workload_execution BEFORE INSERT ON job_runs FOR EACH ROW EXECUTE FUNCTION guard_retired_workload_execution();
CREATE TRIGGER zz_retired_workload_execution BEFORE INSERT OR UPDATE ON job_tasks FOR EACH ROW EXECUTE FUNCTION guard_retired_workload_execution();

CREATE FUNCTION environment_workload_retirement_drain_authorized(d deployments) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT EXISTS(SELECT 1 FROM environment_workload_retirements r JOIN active_environment_git_sources s ON s.id=r.source_id
  JOIN environment_gitops_jobs j ON j.source_id=s.id JOIN project_environments e ON e.id=r.environment_id
  WHERE r.phase='retiring' AND r.app_id=(d).app_id AND e.slug=(d).scope AND (d).id=ANY(r.deployment_ids)
   AND environment_workload_retirement_routes_withdrawn(r) AND j.desired_generation=s.generation AND j.claimed_generation=s.generation
   AND j.lease_until>clock_timestamp() AND j.lease_token=current_setting('gregale.gitops_lease',true))
$$;

CREATE OR REPLACE FUNCTION guard_environment_workload_candidate() RETURNS trigger
    LANGUAGE plpgsql
    AS $_$
DECLARE frozen jsonb; src environment_git_sources%ROWTYPE; allowed boolean;
BEGIN
 frozen:=NEW.environment_workload_runtime;
 IF NEW.status='live' THEN
  IF TG_OP='INSERT' THEN
   IF EXISTS(SELECT 1 FROM deployments d WHERE d.app_id=NEW.app_id AND d.scope=NEW.scope AND d.environment_workload_runtime IS NOT NULL)
    AND NOT environment_workload_activation_authorized(NEW) THEN
    RAISE EXCEPTION 'GitOps-managed workload promotion requires graph activation' USING ERRCODE='23514';
   END IF;
  ELSIF TG_OP='UPDATE' THEN
   IF OLD.status IS DISTINCT FROM 'live' AND
    EXISTS(SELECT 1 FROM deployments d WHERE d.app_id=NEW.app_id AND d.scope=NEW.scope AND d.environment_workload_runtime IS NOT NULL)
    AND NOT environment_workload_activation_authorized(NEW) THEN
    RAISE EXCEPTION 'GitOps-managed workload promotion requires graph activation' USING ERRCODE='23514';
   END IF;
  END IF;
 END IF;
 IF TG_OP='UPDATE' AND OLD.environment_workload_runtime IS NOT NULL AND OLD.status='live' AND
  NEW.status NOT IN ('live','failed') AND NOT (NEW.status='superseded' AND environment_workload_retirement_drain_authorized(OLD)) THEN
  RAISE EXCEPTION 'activated environment workload status is immutable' USING ERRCODE='23514';
 END IF;
 IF TG_OP='UPDATE' AND OLD.environment_workload_runtime IS NOT NULL AND OLD.status='live' AND
  ROW(NEW.rootfs_path,NEW.rootfs_key,NEW.rootfs_bytes) IS DISTINCT FROM ROW(OLD.rootfs_path,OLD.rootfs_key,OLD.rootfs_bytes) THEN
  RAISE EXCEPTION 'activated environment workload artifact is immutable' USING ERRCODE='23514';
 END IF;
 IF TG_OP='UPDATE' AND OLD.environment_workload_runtime IS NOT NULL THEN
  IF NEW.environment_workload_runtime IS DISTINCT FROM OLD.environment_workload_runtime OR
   (OLD.environment_workload_runtime ? 'source_archive' AND ROW(NEW.source_bytes,NEW.source_url,NEW.log_path,NEW.inferred_profile,NEW.github_source_ref,NEW.github_installation_id)
    IS DISTINCT FROM ROW(OLD.source_bytes,OLD.source_url,OLD.log_path,OLD.inferred_profile,OLD.github_source_ref,OLD.github_installation_id)) OR
   (NEW.build_id IS DISTINCT FROM OLD.build_id AND (OLD.build_id IS NOT NULL OR frozen->'source_archive' IS NULL OR
    NEW.build_id IS DISTINCT FROM (frozen->'source_archive'->>'build_id')::uuid OR NOT EXISTS(
     SELECT 1 FROM builds b WHERE b.id=NEW.build_id AND b.deployment_id=NEW.id AND b.kind='github' AND b.source_bytes=NEW.source_bytes))) OR
   environment_workload_deployment_inputs(NEW) IS DISTINCT FROM environment_workload_deployment_inputs(OLD) OR
   ROW(NEW.app_id,NEW.scope,NEW.kind,NEW.image_digest,NEW.commit_sha,NEW.source_path,NEW.source_root,NEW.source_sha256,NEW.handler,
       NEW.override_entrypoint,NEW.override_cmd,NEW.override_env,NEW.override_env_secrets,NEW.override_port,NEW.override_healthcheck,
       NEW.override_liveness_probe,NEW.override_readiness_probe,NEW.override_main_depends_on,NEW.release_command,NEW.release_command_shell)
   IS DISTINCT FROM
   ROW(OLD.app_id,OLD.scope,OLD.kind,OLD.image_digest,OLD.commit_sha,OLD.source_path,OLD.source_root,OLD.source_sha256,OLD.handler,
       OLD.override_entrypoint,OLD.override_cmd,OLD.override_env,OLD.override_env_secrets,OLD.override_port,OLD.override_healthcheck,
       OLD.override_liveness_probe,OLD.override_readiness_probe,OLD.override_main_depends_on,OLD.release_command,OLD.release_command_shell) THEN
   RAISE EXCEPTION 'frozen environment workload inputs are immutable' USING ERRCODE='23514';
  END IF;
 ELSIF frozen IS NOT NULL THEN
  IF TG_OP<>'INSERT' THEN RAISE EXCEPTION 'environment workload inputs must be created atomically' USING ERRCODE='23514'; END IF;
  SELECT * INTO src FROM active_environment_git_sources WHERE id=(frozen->>'source_id')::uuid FOR UPDATE;
  allowed:=src.id IS NOT NULL AND src.mode='enforce' AND NOT src.suspended AND src.generation=(frozen->>'generation')::bigint
   AND src.intent_version=(frozen->>'intent_version')::bigint AND src.environment_id=(frozen->>'environment_id')::uuid
   AND src.approved_revision_id=(frozen->>'revision_id')::uuid AND EXISTS(SELECT 1 FROM environment_gitops_jobs j
    WHERE j.source_id=src.id AND j.desired_generation=src.generation AND j.claimed_generation=src.generation
     AND j.lease_until>clock_timestamp() AND j.lease_token<>'' AND j.lease_token=current_setting('gregale.gitops_lease',true));
  IF NOT allowed OR NOT EXISTS(SELECT 1 FROM environment_gitops_resources r JOIN apps a ON a.id=r.app_id
   JOIN project_environments e ON e.id=src.environment_id LEFT JOIN app_environment_workload_intents w ON w.app_id=a.id AND w.environment_id=e.id
   JOIN environment_desired_revisions rev ON rev.id=src.approved_revision_id AND rev.source_id=src.id
   WHERE r.source_id=src.id AND r.logical_name=frozen->>'resource' AND r.app_id=NEW.app_id AND a.status IN ('active','evicted_cold') AND (a.type='app' OR a.type='function' AND coalesce(a.runtime,'') IN ('node22','python312','go124','go124-alpine','node24','python313'))
    AND a.account_id=src.account_id AND a.project_id=src.project_id AND e.account_id=src.account_id AND e.project_id=src.project_id
    AND NEW.app_id=(frozen->>'app_id')::uuid AND NEW.scope=frozen->>'scope' AND NEW.scope=e.slug AND NEW.status='pending' AND (
     (NEW.kind='image' AND (
       (w.source->>'kind'='image' AND NEW.image_digest=w.source->>'image') OR
       (w.source IS NULL AND frozen ? 'deployment_inputs' AND jsonb_array_length(coalesce(frozen->'source_deployments','[]'::jsonb))>0
        AND NOT EXISTS(SELECT 1 FROM deployments d WHERE d.app_id=a.id AND d.scope=e.slug AND d.status='live'
         AND (d.kind<>'image' OR d.image_digest IS DISTINCT FROM NEW.image_digest))))
      AND NEW.image_digest ~ '^[^[:space:]]+@sha256:[a-f0-9]{64}$') OR
     (NEW.kind='github' AND w.source->>'kind' IN ('source','dockerfile','function') AND
      (w.source->>'kind'<>'function' OR a.type='function' AND a.runtime=w.source->>'runtime') AND w.source_revision=rev.commit_sha
      AND EXISTS(SELECT 1 FROM environment_managed_fields f WHERE f.source_id=src.id AND f.resource=r.logical_name AND f.field_path='source') AND frozen->'source'=w.source AND
      jsonb_typeof(frozen->'source_archive')='object' AND frozen->'source_archive' ?& ARRAY['build_id','path','sha256','bytes','log_path','revision_id','commit_sha','definition_digest'] AND
      frozen->'source_archive'->>'revision_id'=rev.id::text AND frozen->'source_archive'->>'commit_sha'=rev.commit_sha AND
      frozen->'source_archive'->>'definition_digest'=rev.definition_digest AND frozen->>'definition_digest'=rev.definition_digest AND
      (frozen->'source_archive'->>'build_id')::uuid IS NOT NULL AND substring(frozen->'source_archive'->>'build_id',15,1)='7' AND
      NEW.source_root=w.source->>'directory' AND NEW.source_path=frozen->'source_archive'->>'path' AND NEW.source_path LIKE '/%' AND
      NEW.source_sha256=frozen->'source_archive'->>'sha256' AND NEW.source_sha256 ~ '^[a-f0-9]{64}$' AND
      NEW.source_bytes=(frozen->'source_archive'->>'bytes')::bigint AND NEW.source_bytes>0 AND
      NEW.log_path=frozen->'source_archive'->>'log_path' AND NEW.log_path LIKE '/%' AND
      NEW.source_url='github://'||src.repository||'@'||rev.commit_sha AND NEW.build_id IS NULL AND NEW.inferred_profile IS NULL AND
      coalesce(NEW.github_source_ref,'')='' AND coalesce(NEW.image_digest,'')=''))
    AND NEW.commit_sha=rev.commit_sha AND frozen->'runtime'=coalesce(w.runtime,'{}'::jsonb) AND coalesce(frozen->'service_bindings','{}'::jsonb)=coalesce(w.service_bindings,'{}'::jsonb)
    AND coalesce(frozen->'variables','{}'::jsonb)=coalesce(w.variables,'{}'::jsonb)
    AND coalesce(frozen->'schedule','null'::jsonb)=coalesce(w.schedule,'null'::jsonb)
    AND jsonb_strip_nulls(frozen->'baseline')=jsonb_strip_nulls(a.manifest)
    AND frozen->>'start_command'=coalesce(a.start_command,'')
    AND frozen->>'app_type'=a.type AND frozen->>'runtime_base'=coalesce(a.runtime,'') AND frozen->>'workload_class'=a.workload_class
    AND coalesce(a.manifest->'env','{}'::jsonb) IN ('{}'::jsonb,'null'::jsonb) AND coalesce(a.manifest->'service_bindings','[]'::jsonb) IN ('[]'::jsonb,'null'::jsonb)
    AND environment_workload_candidate_queue_bindings_valid(frozen, NEW.app_id)
    AND environment_workload_candidate_secret_refs_valid(frozen, NEW.app_id, NEW.scope, rev.definition->'workloads'->substr(r.logical_name,10)->'secret_refs')
    AND (EXISTS(SELECT 1 FROM environment_managed_fields f WHERE f.source_id=src.id AND f.resource=r.logical_name AND (f.field_path IN ('source','source_revision','schedule') OR starts_with(f.field_path,'runtime/') OR starts_with(f.field_path,'service_bindings/') OR starts_with(f.field_path,'queue_bindings/') OR starts_with(f.field_path,'variables/') OR starts_with(f.field_path,'secret_refs/')))
     OR environment_workload_candidate_applied_removal_valid(frozen))) THEN
   RAISE EXCEPTION 'environment workload preparation lost its reviewed authority' USING ERRCODE='23514';
  END IF;
  IF frozen ? 'deployment_inputs' AND (
   jsonb_typeof(frozen->'deployment_inputs') IS DISTINCT FROM 'object' OR
   frozen->'deployment_inputs' IS DISTINCT FROM environment_workload_deployment_inputs(NEW) OR
   frozen->'source_deployments' IS DISTINCT FROM coalesce((SELECT jsonb_agg(d.id::text ORDER BY d.id)
    FROM deployments d WHERE d.app_id=NEW.app_id AND d.scope=NEW.scope AND d.status='live'),'[]'::jsonb) OR
   NOT EXISTS(SELECT 1 FROM deployments d WHERE d.app_id=NEW.app_id AND d.scope=NEW.scope AND d.status='live') OR
   EXISTS(SELECT 1 FROM deployments d WHERE d.app_id=NEW.app_id AND d.scope=NEW.scope AND d.status='live'
    AND environment_workload_deployment_inputs(d) IS DISTINCT FROM frozen->'deployment_inputs')) THEN
   RAISE EXCEPTION 'inherited environment workload inputs changed after review' USING ERRCODE='23514';
  END IF;
 END IF;
 IF frozen IS NOT NULL AND NEW.status='live' THEN
  IF TG_OP='INSERT' OR OLD.status IS DISTINCT FROM 'live' THEN
   IF NOT environment_workload_activation_authorized(NEW) THEN
    RAISE EXCEPTION 'environment workload graph is not qualified for activation' USING ERRCODE='23514';
   END IF;
  END IF;
 END IF;
 RETURN NEW;
END $_$;

CREATE OR REPLACE FUNCTION guard_environment_workload_instance() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE q environment_workload_qualification_requests%ROWTYPE; app_ram integer; may_deploy boolean; b environment_qualification_restore_reservations%ROWTYPE;
BEGIN
 IF TG_OP='INSERT' AND EXISTS(SELECT 1 FROM environment_qualification_executions WHERE instance_id=NEW.id) THEN
  RAISE EXCEPTION 'retained qualification instance cannot be reused' USING ERRCODE='23514';
 END IF;
 IF TG_OP='DELETE' THEN
  IF EXISTS(SELECT 1 FROM deployments d
   JOIN environment_git_sources s ON s.id=(d.environment_workload_runtime->>'source_id')::uuid
   JOIN project_environments e ON e.id=(d.environment_workload_runtime->>'environment_id')::uuid WHERE d.id=OLD.deployment_id) THEN
   SELECT * INTO q FROM environment_workload_qualification_requests WHERE reserved_instance_id=OLD.id OR id=(SELECT request_id FROM environment_qualification_restore_reservations WHERE instance_id=OLD.id) FOR UPDATE;
   IF OLD.state NOT IN ('parked','stopped','failed') OR (q.id IS NOT NULL AND q.lease_until>clock_timestamp()) THEN
    RAISE EXCEPTION 'environment qualification reservation must remain until retired and its attempt released' USING ERRCODE='23514';
   END IF;
  END IF;
  RETURN OLD;
 END IF;
 IF TG_OP='UPDATE' AND EXISTS(SELECT 1 FROM deployments WHERE id=OLD.deployment_id AND environment_workload_runtime IS NOT NULL)
  AND ROW(NEW.id,NEW.app_id,NEW.deployment_id,NEW.node_id,NEW.wake_id,NEW.mode,NEW.ram_mb,NEW.kind,NEW.job_id)
   IS DISTINCT FROM ROW(OLD.id,OLD.app_id,OLD.deployment_id,OLD.node_id,OLD.wake_id,OLD.mode,OLD.ram_mb,OLD.kind,OLD.job_id) THEN
  RAISE EXCEPTION 'environment qualification instance identity is immutable' USING ERRCODE='23514';
 END IF;
 SELECT * INTO b FROM environment_qualification_restore_reservations WHERE instance_id=NEW.id;
 SELECT * INTO q FROM environment_workload_qualification_requests WHERE reserved_instance_id=NEW.id OR
  (b.instance_id IS NOT NULL AND id=b.request_id AND reserved_instance_id=b.capture_instance_id AND attempt=b.attempt);
 IF q.id IS NOT NULL AND (q.app_id IS DISTINCT FROM NEW.app_id OR q.deployment_id IS DISTINCT FROM NEW.deployment_id) THEN
  RAISE EXCEPTION 'environment qualification reservation cannot be reassigned' USING ERRCODE='23514';
 END IF;
 IF TG_OP='UPDATE' AND q.id IS NULL AND NEW.state IN ('draining','snapshotting','parked','stopped','failed','evicting_account_deleting') AND
  EXISTS(SELECT 1 FROM environment_workload_retirements r JOIN deployments d ON d.id=OLD.deployment_id
   WHERE d.id=ANY(r.deployment_ids) AND d.status='superseded' AND NOT d.environment_workload_held AND r.phase NOT IN ('planned','cancelled')) THEN RETURN NEW; END IF;
 IF q.id IS NULL AND EXISTS(SELECT 1 FROM deployments WHERE id=NEW.deployment_id AND app_id=NEW.app_id
  AND environment_workload_runtime IS NOT NULL AND NOT environment_workload_held AND status='live') THEN RETURN NEW; END IF;
 IF NOT EXISTS(SELECT 1 FROM deployments WHERE id=NEW.deployment_id AND environment_workload_runtime IS NOT NULL) THEN RETURN NEW; END IF;
 IF TG_OP='UPDATE' AND NOT EXISTS(SELECT 1 FROM deployments WHERE id=OLD.deployment_id AND environment_workload_runtime IS NOT NULL) THEN
  RAISE EXCEPTION 'environment qualification instances must be created under their attempt' USING ERRCODE='23514';
 END IF;
 -- Retirement must remain available after expiry, supersession and parent
 -- deletion. It cannot grant execution or qualification evidence.
 IF TG_OP='UPDATE' AND NEW.state IN ('parked','stopped','failed','evicting_account_deleting') THEN RETURN NEW; END IF;
 IF q.id IS NOT NULL THEN
  PERFORM s.id FROM environment_git_sources s JOIN environment_workload_graphs g ON g.source_id=s.id
   WHERE g.id=q.graph_id FOR UPDATE OF s;
  PERFORM a.id FROM apps a JOIN environment_workload_graphs g ON g.id=q.graph_id
   JOIN LATERAL jsonb_array_elements(g.members) m ON a.id=(m->>'app_id')::uuid ORDER BY a.id FOR UPDATE OF a;
  SELECT * INTO q FROM environment_workload_qualification_requests WHERE id=q.id AND deployment_id=NEW.deployment_id FOR UPDATE;
 END IF;
 IF q.id IS NULL OR q.phase<>'claimed' OR (q.execution_mode='job' AND
    (coalesce(jsonb_typeof(q.frozen_inputs->'job_smoke'),'null')<>'object' OR
     coalesce(nullif(q.frozen_inputs->'queue_bindings','null'::jsonb),'{}'::jsonb)<>'{}'::jsonb
     OR NOT EXISTS(SELECT 1 FROM environment_workload_graphs g, jsonb_array_elements(g.members) member
       WHERE g.id=q.graph_id AND member->>'resource'=q.resource AND member->>'execution_mode'='job'
        AND coalesce((member->>'job_smoke_configured')::boolean,false)
        AND coalesce(nullif(member->'service_bindings','null'::jsonb),'{}'::jsonb)=coalesce(nullif(q.frozen_inputs->'service_bindings','null'::jsonb),'{}'::jsonb)
        AND coalesce((member->>'service_bindings_configured')::boolean,false)=(coalesce(nullif(q.frozen_inputs->'service_bindings','null'::jsonb),'{}'::jsonb)<>'{}'::jsonb)
        AND coalesce(member->>'queue_bindings_configured','false')='false'
        AND coalesce(nullif(member->'queue_bindings','null'::jsonb),'{}'::jsonb)='{}'::jsonb
        AND coalesce(nullif(member->'queue_modes','null'::jsonb),'{}'::jsonb)='{}'::jsonb))) OR q.lease_until<=clock_timestamp()
  OR q.lease_token IS DISTINCT FROM current_setting('gregale.gitops_qualification',true)
  OR q.app_id<>NEW.app_id OR NEW.kind<>'wake' OR NEW.job_id IS NOT NULL
  OR NEW.mode IS DISTINCT FROM (CASE q.execution_mode WHEN 'worker' THEN 'worker' WHEN 'service' THEN 'service' ELSE 'normal' END)
  OR (b.instance_id IS NOT NULL AND (NOT environment_qualification_restore_current(q.id,b.capture_instance_id)
   OR NEW.wake_id=(SELECT (frame->>'wake_id')::uuid FROM environment_qualification_executions WHERE instance_id=b.capture_instance_id)))
  OR NOT environment_workload_qualification_inputs_current(q.id) THEN
  RAISE EXCEPTION 'environment workload execution requires its current qualification attempt' USING ERRCODE='23514';
 END IF;
 SELECT a.ram_mb,c.status='active' AND c.abuse_hold_at IS NULL INTO app_ram,may_deploy
  FROM apps a JOIN accounts c ON c.id=a.account_id WHERE a.id=NEW.app_id FOR SHARE OF c;
 IF NOT coalesce(may_deploy,false) OR NEW.ram_mb IS DISTINCT FROM app_ram OR NOT EXISTS(
  SELECT 1 FROM compute_nodes WHERE id=NEW.node_id AND active AND lifecycle='active') OR
  (TG_OP='INSERT' AND (NEW.state<>'cold_booting' OR coalesce(NEW.netns,'')<>'' OR NEW.host_ip IS NOT NULL
   OR coalesce(NEW.guest_uid,0)<>0 OR NEW.framework_ready_at IS NOT NULL)) THEN
  RAISE EXCEPTION 'environment qualification admission shape is invalid' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;


CREATE FUNCTION guard_workload_retirement_source_lifecycle() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF EXISTS(SELECT 1 FROM environment_workload_retirements r WHERE r.source_id=OLD.id AND r.phase NOT IN ('recovered','cancelled')) AND
  EXISTS(SELECT 1 FROM accounts WHERE id=OLD.account_id AND status<>'deleted_pending') THEN
  IF TG_OP='DELETE' THEN RAISE EXCEPTION 'retained workloads must recover before detaching their source' USING ERRCODE='23514'; END IF;
  IF NEW.detached OR NEW.mode<>'enforce' OR ROW(NEW.account_id,NEW.project_id,NEW.environment_id,NEW.provider,NEW.repository_id,NEW.repository,NEW.installation_id)
   IS DISTINCT FROM ROW(OLD.account_id,OLD.project_id,OLD.environment_id,OLD.provider,OLD.repository_id,OLD.repository,OLD.installation_id) THEN
   RAISE EXCEPTION 'retained workloads must recover before changing their source identity' USING ERRCODE='23514';
  END IF;
 END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF;RETURN NEW;
END $$;



CREATE FUNCTION environment_workload_retirement_withdrawal_authorized(d deployments) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT EXISTS(SELECT 1 FROM environment_workload_retirements r JOIN project_environments e ON e.id=r.environment_id
  WHERE r.phase='retiring' AND r.app_id=(d).app_id AND e.slug=(d).scope AND (d).id=ANY(r.deployment_ids)
   AND environment_workload_retirement_cutover_authorized(r))
$$;

CREATE OR REPLACE FUNCTION guard_environment_workload_serving_traffic() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
 IF NEW.traffic_percent IS DISTINCT FROM OLD.traffic_percent AND
    EXISTS(SELECT 1 FROM deployments managed WHERE managed.app_id=OLD.app_id AND managed.scope=OLD.scope AND managed.environment_workload_runtime IS NOT NULL) AND
    NOT environment_workload_serving_traffic_authorized(NEW.app_id,NEW.scope,NEW.id,NEW.traffic_percent) AND
    NOT (NEW.traffic_percent=0 AND NEW.traffic_percent_explicit AND environment_workload_retirement_withdrawal_authorized(OLD)) AND
    NOT (NEW.traffic_percent=0 AND OLD.environment_workload_held AND NOT NEW.environment_workload_held AND
      environment_workload_activation_authorized(NEW)) THEN
  RAISE EXCEPTION 'GitOps-managed workload traffic requires a current serving transition' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;


-- Retained removal keeps the original code identities beyond ordinary release
-- pin expiry. Fresh reviewed recovery releases this retirement-specific pin;
-- existing workflow/operation retention and account teardown still apply.
CREATE OR REPLACE VIEW durable_work_retained_deployment_refs AS
 SELECT deployment_id FROM customer_operation_retained_deployment_refs
 UNION SELECT deployment_id FROM workflow_retained_deployment_refs
 UNION SELECT original.deployment_id FROM environment_workload_retirements r
 JOIN apps a ON a.id=r.app_id AND a.status<>'deleted'
 CROSS JOIN LATERAL unnest(r.deployment_ids) original(deployment_id)
 WHERE r.phase IN ('planned','retiring','retired','recovering');

CREATE TRIGGER zz_workload_retirement_source_lifecycle BEFORE UPDATE OR DELETE ON environment_git_sources
FOR EACH ROW EXECUTE FUNCTION guard_workload_retirement_source_lifecycle();
END $workload_retirement_replay$;
-- +goose StatementEnd

-- +goose Down
-- Forward-only: removing an active retirement fence could reopen retained work.
SELECT 1;
