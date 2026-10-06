-- ADR-430: durable qualification work, separate from serving authority.
-- +goose Up
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION environment_workload_artifact(d deployments) RETURNS jsonb LANGUAGE sql IMMUTABLE AS $$
 SELECT jsonb_build_object('rootfs_path',coalesce((d).rootfs_path,''),'rootfs_key',coalesce((d).rootfs_key,''),
  'rootfs_bytes',coalesce((d).rootfs_bytes,0),'image_digest',coalesce((d).image_digest,''),
  'build_id',coalesce((d).build_id::text,''),'kind',(d).kind,'commit_sha',coalesce((d).commit_sha,''));
$$;
-- +goose StatementEnd
CREATE TABLE IF NOT EXISTS environment_workload_qualification_requests (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 graph_id uuid NOT NULL REFERENCES environment_workload_graphs(id) ON DELETE CASCADE,
 deployment_id uuid NOT NULL REFERENCES deployments(id),
 app_id uuid NOT NULL REFERENCES apps(id),
 resource text NOT NULL CHECK(resource ~ '^workload/[a-z0-9][a-z0-9-]*$'),
 artifact jsonb NOT NULL CHECK(jsonb_typeof(artifact)='object'),
 frozen_inputs jsonb NOT NULL CHECK(jsonb_typeof(frozen_inputs)='object'),
 execution_mode text NOT NULL CHECK(execution_mode IN ('request','service','worker','job')),
 phase text NOT NULL DEFAULT 'queued' CHECK(phase IN ('queued','claimed')),
 created_at timestamptz NOT NULL DEFAULT now(),
 worker_id text NOT NULL DEFAULT '' CHECK(octet_length(worker_id)<=256),
 lease_token text NOT NULL DEFAULT '',
 lease_until timestamptz,
 attempt bigint NOT NULL DEFAULT 0 CHECK(attempt>=0),
 reserved_instance_id uuid,
 CHECK((phase='queued' AND worker_id='' AND lease_token='' AND lease_until IS NULL AND attempt=0) OR
  (phase='claimed' AND worker_id<>'' AND lease_token<>'' AND lease_until IS NOT NULL AND attempt>0)),
 UNIQUE(graph_id,resource), UNIQUE(deployment_id)
);
ALTER TABLE environment_workload_qualification_requests ADD COLUMN IF NOT EXISTS reserved_instance_id uuid;
-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='environment_workload_qualification_requests'::regclass
  AND conname='environment_workload_qualification_reserved_instance_shape') THEN
  ALTER TABLE environment_workload_qualification_requests ADD CONSTRAINT environment_workload_qualification_reserved_instance_shape
   CHECK((phase='queued' AND reserved_instance_id IS NULL) OR (phase='claimed' AND
    ((execution_mode='job' AND reserved_instance_id IS NULL) OR (execution_mode<>'job' AND reserved_instance_id IS NOT NULL))));
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_environment_workload_qualification_request() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE graph environment_workload_graphs%ROWTYPE; src environment_git_sources%ROWTYPE; dep deployments%ROWTYPE;
BEGIN
 IF TG_OP='DELETE' THEN
  IF NOT EXISTS(SELECT 1 FROM environment_workload_graphs WHERE id=OLD.graph_id) THEN RETURN OLD; END IF;
  RAISE EXCEPTION 'qualification work is retained with its reviewed graph' USING ERRCODE='23514';
 END IF;
 IF TG_OP='UPDATE' AND ROW(NEW.id,NEW.graph_id,NEW.deployment_id,NEW.app_id,NEW.resource,NEW.artifact,NEW.frozen_inputs,NEW.execution_mode,NEW.created_at)
  IS DISTINCT FROM ROW(OLD.id,OLD.graph_id,OLD.deployment_id,OLD.app_id,OLD.resource,OLD.artifact,OLD.frozen_inputs,OLD.execution_mode,OLD.created_at) THEN
  RAISE EXCEPTION 'environment qualification inputs are immutable' USING ERRCODE='23514';
 END IF;
 -- The state store takes source, then sorted app locks, before this row.
 SELECT s.* INTO src FROM environment_git_sources s JOIN environment_workload_graphs g ON g.source_id=s.id WHERE g.id=NEW.graph_id FOR UPDATE OF s;
 SELECT * INTO graph FROM environment_workload_graphs WHERE id=NEW.graph_id;
 SELECT * INTO dep FROM deployments WHERE id=NEW.deployment_id;
 IF src.id IS NULL OR src.mode<>'enforce' OR src.suspended OR graph.phase<>'prepared' OR graph.generation<>src.generation OR
  graph.intent_version<>src.intent_version OR graph.revision_id IS DISTINCT FROM src.approved_revision_id OR graph.environment_id<>src.environment_id OR
  dep.status<>'snapshotting' OR coalesce(dep.rootfs_bytes,0)<=0 OR coalesce(dep.rootfs_key,'')='' AND coalesce(dep.rootfs_path,'')='' OR
  NEW.artifact IS DISTINCT FROM environment_workload_artifact(dep) OR NEW.frozen_inputs IS DISTINCT FROM dep.environment_workload_runtime OR
  NEW.execution_mode IS DISTINCT FROM (CASE WHEN NEW.frozen_inputs->'runtime' ? 'execution_mode'
   THEN coalesce(nullif(NEW.frozen_inputs->'runtime'->>'execution_mode',''),'request')
   ELSE coalesce(nullif(NEW.frozen_inputs->'baseline'->>'execution_mode',''),'request') END) OR
  NOT EXISTS(SELECT 1 FROM jsonb_array_elements(graph.members) m WHERE m->>'resource'=NEW.resource AND m->>'app_id'=NEW.app_id::text
   AND m->>'candidate_deployment_id'=NEW.deployment_id::text) OR NOT EXISTS(
   SELECT 1 FROM apps a JOIN project_environments e ON e.id=graph.environment_id
   WHERE a.id=NEW.app_id AND a.id=dep.app_id AND a.status IN ('active','evicted_cold') AND a.account_id=src.account_id AND a.project_id=src.project_id
    AND e.account_id=src.account_id AND e.project_id=src.project_id AND dep.scope=e.slug
    AND jsonb_strip_nulls(NEW.frozen_inputs->'baseline')=jsonb_strip_nulls(a.manifest)
    AND NEW.frozen_inputs->>'start_command'=coalesce(a.start_command,'')
    AND NEW.frozen_inputs->>'app_type'=a.type AND NEW.frozen_inputs->>'runtime_base'=coalesce(a.runtime,'')
    AND NEW.frozen_inputs->>'workload_class'=a.workload_class) THEN
  RAISE EXCEPTION 'environment qualification lost its reviewed graph or artifact' USING ERRCODE='23514';
 END IF;
 IF TG_OP='INSERT' THEN
  IF NEW.phase<>'queued' OR NOT EXISTS(SELECT 1 FROM environment_gitops_jobs j WHERE j.source_id=src.id AND j.desired_generation=src.generation
   AND j.claimed_generation=src.generation AND j.lease_until>clock_timestamp() AND j.lease_token<>''
   AND j.lease_token=current_setting('gregale.gitops_lease',true)) THEN
   RAISE EXCEPTION 'environment qualification publication requires the issued intent lease' USING ERRCODE='23514';
  END IF;
 ELSE
  IF EXISTS(SELECT 1 FROM jsonb_array_elements(graph.members) m LEFT JOIN environment_workload_qualification_requests q
   ON q.graph_id=graph.id AND q.resource=m->>'resource' AND q.deployment_id=(m->>'candidate_deployment_id')::uuid
   LEFT JOIN deployments d ON d.id=q.deployment_id LEFT JOIN apps a ON a.id=d.app_id
   WHERE m ? 'candidate_deployment_id' AND (q.id IS NULL OR d.status IS DISTINCT FROM 'snapshotting'
    OR q.artifact IS DISTINCT FROM environment_workload_artifact(d) OR q.frozen_inputs IS DISTINCT FROM d.environment_workload_runtime
    OR a.status IS NULL OR a.status NOT IN ('active','evicted_cold')
    OR jsonb_strip_nulls(q.frozen_inputs->'baseline') IS DISTINCT FROM jsonb_strip_nulls(a.manifest)
    OR q.frozen_inputs->>'start_command' IS DISTINCT FROM coalesce(a.start_command,'')
    OR q.frozen_inputs->>'app_type' IS DISTINCT FROM a.type OR q.frozen_inputs->>'runtime_base' IS DISTINCT FROM coalesce(a.runtime,'')
    OR q.frozen_inputs->>'workload_class' IS DISTINCT FROM a.workload_class)) THEN
   RAISE EXCEPTION 'environment qualification requires the complete unchanged artifact cohort' USING ERRCODE='23514';
  END IF;
  IF NEW.phase<>'claimed' OR NEW.lease_token IS DISTINCT FROM current_setting('gregale.gitops_qualification',true) OR
   NEW.lease_until<=clock_timestamp() OR NEW.lease_until>clock_timestamp()+interval '15 minutes' OR
   (OLD.phase='claimed' AND OLD.lease_until>clock_timestamp() AND
    (NEW.lease_token<>OLD.lease_token OR NEW.worker_id<>OLD.worker_id OR NEW.attempt<>OLD.attempt OR NEW.lease_until<OLD.lease_until
     OR NEW.reserved_instance_id IS DISTINCT FROM OLD.reserved_instance_id)) OR
   ((OLD.phase='queued' OR OLD.lease_until<=clock_timestamp()) AND
    (NEW.attempt<>OLD.attempt+1 OR NEW.lease_token=OLD.lease_token OR
     OLD.reserved_instance_id IS NOT NULL AND NEW.execution_mode<>'job' AND NEW.reserved_instance_id=OLD.reserved_instance_id OR
     EXISTS(SELECT 1 FROM instances i WHERE i.id=OLD.reserved_instance_id AND i.state NOT IN ('parked','stopped','failed')))) THEN
   RAISE EXCEPTION 'environment qualification execution lease is stale' USING ERRCODE='23514';
  END IF;
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS guard_environment_workload_qualification_request ON environment_workload_qualification_requests;
CREATE TRIGGER guard_environment_workload_qualification_request BEFORE INSERT OR UPDATE OR DELETE ON environment_workload_qualification_requests
 FOR EACH ROW EXECUTE FUNCTION guard_environment_workload_qualification_request();
-- +goose Down
-- Retain the work journal; binary rollback never grants serving authority.
SELECT 1;
