-- ADR-430: durable reviewed graph membership, independent of serving proof.
-- +goose Up
CREATE TABLE IF NOT EXISTS environment_workload_graphs (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 source_id uuid NOT NULL REFERENCES environment_git_sources(id) ON DELETE CASCADE,
 environment_id uuid NOT NULL REFERENCES project_environments(id) ON DELETE CASCADE,
 revision_id uuid NOT NULL REFERENCES environment_desired_revisions(id),
 generation bigint NOT NULL CHECK(generation>0),
 intent_version bigint NOT NULL CHECK(intent_version>=0),
 plan_hash text NOT NULL CHECK(plan_hash ~ '^[a-f0-9]{64}$'),
 definition_digest text NOT NULL CHECK(definition_digest ~ '^[a-f0-9]{64}$'),
 members jsonb NOT NULL CHECK(jsonb_typeof(members)='array'),
 resource_ids jsonb NOT NULL CHECK(jsonb_typeof(resource_ids)='object'),
 phase text NOT NULL DEFAULT 'preparing' CHECK(phase IN ('preparing','prepared','failed')),
 error_code text NOT NULL DEFAULT '' CHECK(error_code IN ('','environment_workload_artifact_failed')),
 created_at timestamptz NOT NULL DEFAULT now(),
 prepared_at timestamptz,
 CHECK((phase='prepared')=(prepared_at IS NOT NULL)),
 CHECK((phase='failed')=(error_code<>'')),
 UNIQUE(source_id,generation,plan_hash)
);

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_environment_workload_graph() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE src environment_git_sources%ROWTYPE; rev environment_desired_revisions%ROWTYPE;
 member jsonb; member_count integer; names jsonb; allowed boolean;
 binding queue_bindings%ROWTYPE; consumer_ids jsonb; binding_resource text;
BEGIN
 IF TG_OP='DELETE' THEN
  IF NOT EXISTS(SELECT 1 FROM environment_git_sources s JOIN project_environments e ON e.id=s.environment_id
   WHERE s.id=OLD.source_id AND e.id=OLD.environment_id) THEN RETURN OLD; END IF;
  RAISE EXCEPTION 'environment graph journal is retained with its original source' USING ERRCODE='23514';
 END IF;
 IF TG_OP='UPDATE' AND ROW(NEW.id,NEW.source_id,NEW.environment_id,NEW.revision_id,NEW.generation,NEW.intent_version,
  NEW.plan_hash,NEW.definition_digest,NEW.members,NEW.resource_ids,NEW.created_at)
  IS DISTINCT FROM ROW(OLD.id,OLD.source_id,OLD.environment_id,OLD.revision_id,OLD.generation,OLD.intent_version,
  OLD.plan_hash,OLD.definition_digest,OLD.members,OLD.resource_ids,OLD.created_at) THEN
  RAISE EXCEPTION 'reviewed environment graph inputs are immutable' USING ERRCODE='23514';
 END IF;
 SELECT * INTO src FROM environment_git_sources WHERE id=NEW.source_id FOR UPDATE;
 allowed:=src.id IS NOT NULL AND src.environment_id=NEW.environment_id AND src.mode='enforce' AND NOT src.suspended
  AND src.generation=NEW.generation AND src.intent_version=NEW.intent_version AND src.approved_revision_id=NEW.revision_id
  AND EXISTS(SELECT 1 FROM environment_gitops_jobs j WHERE j.source_id=src.id AND j.desired_generation=src.generation
   AND j.claimed_generation=src.generation AND j.lease_until>clock_timestamp() AND j.lease_token<>''
   AND j.lease_token=current_setting('gregale.gitops_lease',true));
 SELECT * INTO rev FROM environment_desired_revisions WHERE id=NEW.revision_id AND source_id=NEW.source_id;
 IF NOT coalesce(allowed,false) OR rev.definition_digest IS DISTINCT FROM NEW.definition_digest THEN
  RAISE EXCEPTION 'environment graph preparation lost reviewed authority' USING ERRCODE='23514';
 END IF;
 IF TG_OP='INSERT' AND (NEW.phase<>'preparing' OR NEW.prepared_at IS NOT NULL OR NEW.error_code<>'') THEN
  RAISE EXCEPTION 'environment graph must begin in preparation' USING ERRCODE='23514';
 END IF;
 IF TG_OP='UPDATE' AND (OLD.phase='failed' AND ROW(NEW.phase,NEW.error_code,NEW.prepared_at) IS DISTINCT FROM ROW(OLD.phase,OLD.error_code,OLD.prepared_at)
  OR OLD.phase='prepared' AND NEW.phase='preparing'
  OR OLD.phase=NEW.phase AND ROW(NEW.error_code,NEW.prepared_at) IS DISTINCT FROM ROW(OLD.error_code,OLD.prepared_at)) THEN
  RAISE EXCEPTION 'environment graph preparation transition is invalid' USING ERRCODE='23514';
 END IF;
 SELECT count(DISTINCT value->>'resource'),coalesce(jsonb_agg(value->>'resource' ORDER BY value->>'resource'),'[]')
  INTO member_count,names FROM jsonb_array_elements(NEW.members);
 IF member_count<>jsonb_array_length(NEW.members) OR names IS DISTINCT FROM
  (SELECT coalesce(jsonb_agg('workload/'||key ORDER BY key),'[]') FROM jsonb_object_keys(rev.definition->'workloads') key) THEN
  RAISE EXCEPTION 'environment graph must include every reviewed workload exactly once' USING ERRCODE='23514';
 END IF;
 FOR member IN SELECT value FROM jsonb_array_elements(NEW.members) LOOP
  IF jsonb_typeof(member) IS DISTINCT FROM 'object' OR NOT member ?& ARRAY['resource','app_id','retained_deployments']
   OR jsonb_typeof(member->'retained_deployments') IS DISTINCT FROM 'array' OR NOT EXISTS(
    SELECT 1 FROM environment_gitops_resources r JOIN apps a ON a.id=r.app_id JOIN project_environments e ON e.id=src.environment_id
    WHERE r.source_id=src.id AND r.logical_name=member->>'resource' AND a.id=(member->>'app_id')::uuid
     AND a.account_id=src.account_id AND a.project_id=src.project_id AND a.status IN ('active','evicted_cold')
     AND e.account_id=src.account_id AND e.project_id=src.project_id
     AND NEW.resource_ids->>r.logical_name=a.id::text) OR
   member->'retained_deployments' IS DISTINCT FROM (SELECT coalesce(jsonb_agg(d.id::text ORDER BY d.id),'[]')
    FROM deployments d JOIN project_environments e ON e.id=src.environment_id
    WHERE d.app_id=(member->>'app_id')::uuid AND d.scope=e.slug AND d.status='live') THEN
   RAISE EXCEPTION 'environment graph original workload identities changed' USING ERRCODE='23514';
  END IF;
  IF member ? 'candidate_deployment_id' THEN
   IF NOT EXISTS(SELECT 1 FROM deployments d WHERE d.id=(member->>'candidate_deployment_id')::uuid
    AND d.app_id=(member->>'app_id')::uuid AND d.environment_workload_runtime->>'source_id'=src.id::text
    AND d.environment_workload_runtime->>'environment_id'=src.environment_id::text
    AND d.environment_workload_runtime->>'revision_id'=rev.id::text
    AND d.environment_workload_runtime->>'generation'=NEW.generation::text
    AND d.environment_workload_runtime->>'intent_version'=NEW.intent_version::text
    AND d.environment_workload_runtime->>'resource'=member->>'resource'
    AND d.environment_workload_runtime->>'plan_hash'=NEW.plan_hash
    AND (NEW.phase<>'prepared' OR d.status='snapshotting' AND (coalesce(d.rootfs_path,'')<>'' OR coalesce(d.rootfs_key,'')<>''))) THEN
    RAISE EXCEPTION 'environment graph candidate does not match its reviewed inputs or artifact phase' USING ERRCODE='23514';
   END IF;
  ELSIF EXISTS(SELECT 1 FROM environment_managed_fields f WHERE f.source_id=src.id AND f.resource=member->>'resource'
   AND (f.field_path='source' OR starts_with(f.field_path,'runtime/'))) THEN
   RAISE EXCEPTION 'environment graph is missing a managed workload candidate' USING ERRCODE='23514';
  END IF;
  FOR binding IN SELECT b.* FROM queue_bindings b WHERE b.app_id=(member->>'app_id')::uuid
   AND b.account_id=src.account_id AND b.environment_id=src.environment_id LOOP
   binding_resource:=(member->>'resource')||'/queue_bindings/'||binding.name;
   SELECT coalesce(jsonb_agg(t.id::text ORDER BY t.id),'[]') INTO consumer_ids FROM triggers t WHERE t.queue_binding_id=binding.id;
   IF NEW.resource_ids->>binding_resource IS DISTINCT FROM binding.id::text OR
    jsonb_array_length(consumer_ids)=1 AND NEW.resource_ids->>(binding_resource||'/consumer') IS DISTINCT FROM consumer_ids->>0 OR
    jsonb_array_length(consumer_ids)<>1 AND NEW.resource_ids ? (binding_resource||'/consumer') THEN
    RAISE EXCEPTION 'environment graph original queue and consumer identities changed' USING ERRCODE='23514';
   END IF;
  END LOOP;
 END LOOP;
 RETURN NEW;
END $$;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS guard_environment_workload_graph ON environment_workload_graphs;
CREATE TRIGGER guard_environment_workload_graph BEFORE INSERT OR UPDATE OR DELETE ON environment_workload_graphs
 FOR EACH ROW EXECUTE FUNCTION guard_environment_workload_graph();

-- +goose Down
-- Retain the journal and preparation hold across binary rollback.
SELECT 1;
