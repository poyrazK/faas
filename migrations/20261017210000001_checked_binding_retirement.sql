-- ADR-1548: checked removal of a complete managed graph, without recipients.
-- +goose Up
ALTER TABLE environment_workload_retirements ADD COLUMN binding_policy_context jsonb
 CHECK(binding_policy_context IS NULL OR jsonb_typeof(binding_policy_context)='object');

-- Policy revisions are monotonic, including off -> enforce transitions.
-- Historical reservations stay NULL and cannot be upgraded into authority.
-- +goose StatementBegin
CREATE FUNCTION environment_binding_retirement_policy(target_app uuid,target_scope text) RETURNS jsonb LANGUAGE sql STABLE AS $$
 SELECT coalesce((SELECT jsonb_build_object('revision',revision,'mode',mode)
  FROM app_binding_release_policies WHERE app_id=target_app AND scope=target_scope),jsonb_build_object('revision',0,'mode','off'));
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE FUNCTION guard_environment_binding_retirement_policy() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='INSERT' THEN
  NEW.binding_policy_context:=environment_binding_retirement_policy(NEW.app_id,(SELECT slug FROM project_environments WHERE id=NEW.environment_id));
 ELSIF NEW.binding_policy_context IS DISTINCT FROM OLD.binding_policy_context THEN
  RAISE EXCEPTION 'retirement policy review is immutable; review a fresh generation' USING ERRCODE='23514',CONSTRAINT='binding_release_changed';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER guard_environment_binding_retirement_policy BEFORE INSERT OR UPDATE ON environment_workload_retirements
 FOR EACH ROW EXECUTE FUNCTION guard_environment_binding_retirement_policy();

CREATE TABLE environment_binding_retirement_receipts (
 release_id uuid PRIMARY KEY REFERENCES project_release_sets(id) ON DELETE CASCADE DEFERRABLE INITIALLY DEFERRED,
 source_id uuid NOT NULL REFERENCES environment_git_sources(id) ON DELETE CASCADE,
 revision_id uuid NOT NULL REFERENCES environment_desired_revisions(id) ON DELETE RESTRICT,
 generation bigint NOT NULL CHECK(generation>0),
 intent_version bigint NOT NULL CHECK(intent_version>0),
 plan_hash text NOT NULL CHECK(plan_hash ~ '^[a-f0-9]{64}$'),
 prior_release text NOT NULL CHECK(prior_release='' OR prior_release ~ '^[a-f0-9-]{36}$'),
 retirement_ids uuid[] NOT NULL CHECK(cardinality(retirement_ids)>0 AND array_position(retirement_ids,NULL) IS NULL),
 policies jsonb NOT NULL CHECK(jsonb_typeof(policies)='object'),
 valid_until timestamptz NOT NULL,
 recorded_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
-- +goose StatementBegin
CREATE FUNCTION guard_environment_binding_retirement_audit() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' AND pg_trigger_depth()>1 THEN RETURN OLD; END IF;
 RAISE EXCEPTION 'checked binding retirement audit is immutable' USING ERRCODE='23514';
END $$;
-- +goose StatementEnd
CREATE TRIGGER guard_environment_binding_retirement_audit BEFORE UPDATE OR DELETE ON environment_binding_retirement_receipts
 FOR EACH ROW EXECUTE FUNCTION guard_environment_binding_retirement_audit();

-- No probes against removed deployments are needed: this grant has no recipient.
-- Original retirement guards still own inventory, ownership, consumer pause and
-- withdrawal; the deferred check also fences same-transaction catalogue drift.
-- +goose StatementBegin
CREATE FUNCTION environment_binding_retirement_current(audit environment_binding_retirement_receipts) RETURNS boolean LANGUAGE plpgsql AS $$
DECLARE s environment_git_sources%ROWTYPE; rev environment_desired_revisions%ROWTYPE; release_scope text;
 r environment_workload_retirements%ROWTYPE; ids uuid[]; policy_map jsonb;
BEGIN
 SELECT * INTO s FROM active_environment_git_sources WHERE id=audit.source_id;
 SELECT * INTO rev FROM environment_desired_revisions WHERE id=s.approved_revision_id AND source_id=s.id;
 SELECT slug INTO release_scope FROM project_environments WHERE id=s.environment_id;
 IF s.id IS NULL OR s.mode<>'enforce' OR s.suspended OR NOT s.prune OR rev.id IS DISTINCT FROM audit.revision_id OR
  s.generation<>audit.generation OR s.intent_version<>audit.intent_version OR audit.valid_until<=clock_timestamp() OR
  rev.definition->'workloads' IS DISTINCT FROM '{}'::jsonb OR rev.definition->>'workload_pruning_policy' IS DISTINCT FROM 'retain' OR
  NOT EXISTS(SELECT 1 FROM environment_gitops_jobs j WHERE j.source_id=s.id AND j.desired_generation=s.generation
   AND j.claimed_generation=s.generation AND j.lease_token=current_setting('gregale.gitops_lease',true) AND j.lease_until>clock_timestamp()) THEN RETURN false; END IF;
 SELECT array_agg(id ORDER BY id),jsonb_object_agg(id::text,binding_policy_context) INTO ids,policy_map
 FROM environment_workload_retirements WHERE source_id=s.id AND revision_id=rev.id AND generation=s.generation AND phase='retiring';
 IF ids IS DISTINCT FROM audit.retirement_ids OR policy_map IS DISTINCT FROM audit.policies THEN RETURN false; END IF;
 FOR r IN SELECT * FROM environment_workload_retirements WHERE id=ANY(audit.retirement_ids) LOOP
  IF r.binding_policy_context IS DISTINCT FROM environment_binding_retirement_policy(r.app_id,release_scope) OR
   ROW(r.account_id,r.project_id,r.environment_id) IS DISTINCT FROM ROW(s.account_id,s.project_id,s.environment_id) OR
   NOT EXISTS(SELECT 1 FROM environment_gitops_resources WHERE source_id=s.id AND logical_name=r.resource AND app_id=r.app_id) OR
   NOT EXISTS(SELECT 1 FROM environment_managed_fields WHERE source_id=s.id AND manager_id=s.id::text AND resource=r.resource AND field_path='presence') OR
   EXISTS(SELECT 1 FROM environment_managed_fields WHERE environment_id=s.environment_id AND resource=r.resource AND manager_id<>s.id::text) OR
   EXISTS(SELECT 1 FROM environment_external_field_owners WHERE environment_id=s.environment_id AND resource IN (r.resource,'app/'||r.app_id::text)) OR
   EXISTS(SELECT 1 FROM environment_management_overrides WHERE environment_id=s.environment_id AND resource=r.resource AND expires_at>clock_timestamp()) OR
   r.deployment_ids IS DISTINCT FROM ARRAY(SELECT id FROM deployments WHERE app_id=r.app_id AND scope=release_scope AND environment_workload_runtime->>'source_id'=s.id::text ORDER BY id) OR
   r.queue_binding_ids IS DISTINCT FROM ARRAY(SELECT binding_id FROM environment_gitops_queue_bindings WHERE source_id=s.id AND resource=r.resource AND binding_id IS NOT NULL ORDER BY binding_id) OR
   r.job_id IS DISTINCT FROM (SELECT job_id FROM app_environment_workload_intents WHERE app_id=r.app_id AND environment_id=s.environment_id) OR
   EXISTS(SELECT 1 FROM deployments WHERE app_id=r.app_id AND scope=release_scope AND status='live' AND (environment_workload_runtime->>'source_id' IS DISTINCT FROM s.id::text OR traffic_percent<>0)) OR
   EXISTS(SELECT 1 FROM queue_bindings WHERE app_id=r.app_id AND environment_id=s.environment_id AND NOT(id=ANY(r.queue_binding_ids))) THEN RETURN false; END IF;
 END LOOP;
 -- Complete removal only. Any surviving caller or recipient needs a separately
 -- qualified replacement graph; this path cannot retain or redistribute traffic.
 IF EXISTS(SELECT 1 FROM apps a WHERE a.project_id=s.project_id AND a.account_id=s.account_id AND a.status<>'deleted' AND coalesce(a.preview_of_slug,'')=''
  AND NOT EXISTS(SELECT 1 FROM environment_workload_retirements owned WHERE owned.source_id=s.id AND owned.app_id=a.id AND owned.environment_id=s.environment_id
   AND (owned.id=ANY(audit.retirement_ids) AND owned.phase='retiring' OR owned.phase='retired'))) THEN RETURN false; END IF;
 RETURN true;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION authorize_environment_binding_retirement(target_source uuid,next_release uuid,prior_release text,reviewed_plan text) RETURNS boolean LANGUAGE plpgsql AS $$
DECLARE s environment_git_sources%ROWTYPE; audit environment_binding_retirement_receipts%ROWTYPE; release_scope text; latest text;
BEGIN
 SELECT * INTO s FROM active_environment_git_sources WHERE id=target_source FOR UPDATE;
 IF s.id IS NULL THEN RAISE EXCEPTION 'retirement source changed' USING ERRCODE='23514',CONSTRAINT='binding_release_changed'; END IF;
 PERFORM id FROM projects WHERE id=s.project_id AND account_id=s.account_id FOR UPDATE;
 PERFORM id FROM apps WHERE project_id=s.project_id ORDER BY id FOR UPDATE;
 PERFORM r.app_id FROM app_binding_promotion_revisions r JOIN apps a ON a.id=r.app_id WHERE a.project_id=s.project_id ORDER BY r.app_id FOR UPDATE OF r;
 SELECT slug INTO release_scope FROM project_environments WHERE id=s.environment_id;
 SELECT id::text INTO latest FROM project_release_sets WHERE project_id=s.project_id AND environment_slug=release_scope AND active;
 IF coalesce(latest,'')<>prior_release THEN RAISE EXCEPTION 'active graph changed' USING ERRCODE='23514',CONSTRAINT='binding_release_changed'; END IF;
 audit.release_id:=next_release; audit.source_id:=s.id; audit.revision_id:=s.approved_revision_id; audit.generation:=s.generation;
 audit.intent_version:=s.intent_version; audit.plan_hash:=reviewed_plan; audit.prior_release:=prior_release;
 SELECT array_agg(id ORDER BY id),jsonb_object_agg(id::text,binding_policy_context) INTO audit.retirement_ids,audit.policies
 FROM environment_workload_retirements WHERE source_id=s.id AND revision_id=s.approved_revision_id AND generation=s.generation AND phase='retiring';
 SELECT lease_until INTO audit.valid_until FROM environment_gitops_jobs WHERE source_id=s.id AND lease_token=current_setting('gregale.gitops_lease',true);
 IF audit.retirement_ids IS NULL OR audit.valid_until IS NULL OR NOT environment_binding_retirement_current(audit) THEN
  RAISE EXCEPTION 'complete removal requires current reviewed binding retirement authority' USING ERRCODE='23514',CONSTRAINT='binding_release_required'; END IF;
 INSERT INTO environment_binding_retirement_receipts(release_id,source_id,revision_id,generation,intent_version,plan_hash,prior_release,retirement_ids,policies,valid_until)
 VALUES(audit.release_id,audit.source_id,audit.revision_id,audit.generation,audit.intent_version,audit.plan_hash,audit.prior_release,audit.retirement_ids,audit.policies,audit.valid_until);
 PERFORM set_config('gregale.binding_release_graph',jsonb_build_object('project_id',s.project_id,'environment',release_scope,'new_id',next_release,'old_id',prior_release,'members','{}'::jsonb,'fences',jsonb_build_array(jsonb_build_object('valid_until',audit.valid_until)))::text,true);
 RETURN true;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION validate_environment_binding_retirement() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NOT environment_binding_retirement_current(NEW) OR EXISTS(SELECT 1 FROM project_release_members WHERE release_id=NEW.release_id) OR
  NOT EXISTS(SELECT 1 FROM project_release_sets rs JOIN environment_git_sources s ON s.id=NEW.source_id JOIN project_environments e ON e.id=s.environment_id
   WHERE rs.id=NEW.release_id AND rs.project_id=s.project_id AND rs.account_id=s.account_id AND rs.environment_slug=e.slug AND rs.active) THEN
  RAISE EXCEPTION 'binding retirement authority changed before commit' USING ERRCODE='23514',CONSTRAINT='binding_release_required'; END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE CONSTRAINT TRIGGER validate_environment_binding_retirement AFTER INSERT ON environment_binding_retirement_receipts
 DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION validate_environment_binding_retirement();

-- +goose Down
-- Preserve reviewed policy snapshots and the removal audit across downgrades.
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM environment_binding_retirement_receipts) OR EXISTS(SELECT 1 FROM environment_workload_retirements WHERE binding_policy_context->>'mode'='enforce') THEN
  RAISE EXCEPTION 'cannot discard checked binding retirement history'; END IF;
END $$;
-- +goose StatementEnd
DROP TRIGGER validate_environment_binding_retirement ON environment_binding_retirement_receipts;
DROP FUNCTION validate_environment_binding_retirement();
DROP FUNCTION authorize_environment_binding_retirement(uuid,uuid,text,text);
DROP FUNCTION environment_binding_retirement_current(environment_binding_retirement_receipts);
DROP TABLE environment_binding_retirement_receipts;
DROP FUNCTION guard_environment_binding_retirement_audit();
DROP TRIGGER guard_environment_binding_retirement_policy ON environment_workload_retirements;
DROP FUNCTION guard_environment_binding_retirement_policy();
DROP FUNCTION environment_binding_retirement_policy(uuid,text);
ALTER TABLE environment_workload_retirements DROP COLUMN binding_policy_context;
