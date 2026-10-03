-- ADR-431: schedd-owned admission for one exact qualification attempt.
-- +goose Up
CREATE UNIQUE INDEX IF NOT EXISTS environment_qualification_reserved_instance_unique_idx
 ON environment_workload_qualification_requests(reserved_instance_id) WHERE reserved_instance_id IS NOT NULL;
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION environment_workload_qualification_inputs_current(request_id uuid) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT EXISTS(SELECT 1 FROM environment_workload_qualification_requests target
  JOIN environment_workload_graphs g ON g.id=target.graph_id JOIN environment_git_sources s ON s.id=g.source_id
  JOIN project_environments e ON e.id=g.environment_id JOIN accounts c ON c.id=s.account_id
  WHERE target.id=request_id AND g.phase='prepared' AND s.mode='enforce' AND NOT s.suspended
   AND c.status='active' AND c.abuse_hold_at IS NULL
   AND g.generation=s.generation AND g.intent_version=s.intent_version AND g.revision_id=s.approved_revision_id
   AND g.environment_id=s.environment_id AND e.account_id=s.account_id AND e.project_id=s.project_id
   AND NOT EXISTS(SELECT 1 FROM jsonb_array_elements(g.members) m LEFT JOIN apps a ON a.id=(m->>'app_id')::uuid
    WHERE a.id IS NULL OR a.status NOT IN ('active','evicted_cold') OR a.account_id<>s.account_id OR a.project_id<>s.project_id
     OR m->'retained_deployments' IS DISTINCT FROM (SELECT coalesce(jsonb_agg(d.id::text ORDER BY d.id),'[]'::jsonb)
      FROM deployments d WHERE d.app_id=a.id AND d.scope=e.slug AND d.status='live'))
   AND NOT EXISTS(SELECT 1 FROM jsonb_array_elements(g.members) m
    LEFT JOIN environment_workload_qualification_requests q ON q.graph_id=g.id AND q.resource=m->>'resource'
     AND q.app_id=(m->>'app_id')::uuid AND q.deployment_id=(m->>'candidate_deployment_id')::uuid
    LEFT JOIN deployments d ON d.id=q.deployment_id LEFT JOIN apps a ON a.id=q.app_id
    WHERE m ? 'candidate_deployment_id' AND (q.id IS NULL OR d.status IS DISTINCT FROM 'snapshotting' OR coalesce(d.rootfs_bytes,0)<=0
     OR q.artifact IS DISTINCT FROM environment_workload_artifact(d) OR q.frozen_inputs IS DISTINCT FROM d.environment_workload_runtime
     OR d.scope IS DISTINCT FROM e.slug OR d.app_id IS DISTINCT FROM a.id
     OR jsonb_strip_nulls(q.frozen_inputs->'baseline') IS DISTINCT FROM jsonb_strip_nulls(a.manifest)
     OR q.frozen_inputs->>'start_command' IS DISTINCT FROM coalesce(a.start_command,'') OR q.frozen_inputs->>'app_type' IS DISTINCT FROM a.type
     OR q.frozen_inputs->>'runtime_base' IS DISTINCT FROM coalesce(a.runtime,'') OR q.frozen_inputs->>'workload_class' IS DISTINCT FROM a.workload_class
     OR q.frozen_inputs ? 'deployment_inputs' AND EXISTS(SELECT 1 FROM deployments original
      WHERE original.app_id=a.id AND original.scope=e.slug AND original.status='live'
       AND environment_workload_deployment_inputs(original) IS DISTINCT FROM q.frozen_inputs->'deployment_inputs'))));
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_environment_workload_instance() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE q environment_workload_qualification_requests%ROWTYPE; app_ram integer; may_deploy boolean;
BEGIN
 IF TG_OP='DELETE' THEN
  IF EXISTS(SELECT 1 FROM deployments d
   JOIN environment_git_sources s ON s.id=(d.environment_workload_runtime->>'source_id')::uuid
   JOIN project_environments e ON e.id=(d.environment_workload_runtime->>'environment_id')::uuid WHERE d.id=OLD.deployment_id) THEN
   SELECT * INTO q FROM environment_workload_qualification_requests WHERE reserved_instance_id=OLD.id FOR UPDATE;
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
 SELECT * INTO q FROM environment_workload_qualification_requests WHERE reserved_instance_id=NEW.id;
 IF q.id IS NOT NULL AND (q.app_id IS DISTINCT FROM NEW.app_id OR q.deployment_id IS DISTINCT FROM NEW.deployment_id) THEN
  RAISE EXCEPTION 'environment qualification reservation cannot be reassigned' USING ERRCODE='23514';
 END IF;
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
  SELECT * INTO q FROM environment_workload_qualification_requests WHERE reserved_instance_id=NEW.id AND deployment_id=NEW.deployment_id FOR UPDATE;
 END IF;
 IF q.id IS NULL OR q.phase<>'claimed' OR q.execution_mode='job' OR q.lease_until<=clock_timestamp()
  OR q.lease_token IS DISTINCT FROM current_setting('gregale.gitops_qualification',true)
  OR q.app_id<>NEW.app_id OR NEW.kind<>'wake' OR NEW.job_id IS NOT NULL
  OR NEW.mode IS DISTINCT FROM (CASE q.execution_mode WHEN 'worker' THEN 'worker' WHEN 'service' THEN 'service' ELSE 'normal' END)
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
-- +goose StatementEnd
DROP TRIGGER IF EXISTS guard_environment_workload_instance ON instances;
CREATE TRIGGER guard_environment_workload_instance BEFORE INSERT OR DELETE OR UPDATE OF id,app_id,deployment_id,node_id,wake_id,mode,ram_mb,kind,job_id,state,netns,host_ip,guest_uid,framework_ready_at ON instances
 FOR EACH ROW EXECUTE FUNCTION guard_environment_workload_instance();
-- +goose Down
-- Retain the fenced admission contract when rolling back binaries.
SELECT 1;
