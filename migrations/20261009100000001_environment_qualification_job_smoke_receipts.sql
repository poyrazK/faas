-- Persist sanitized success evidence for the attempt-bound one-shot job path.
-- +goose Up
-- Job candidate instances share the durable admission/retirement ledger, and
-- bound jobs are held until their attempt-bound route and config receipt exist.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_environment_workload_instance() RETURNS trigger LANGUAGE plpgsql AS $$
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
 IF NOT EXISTS(SELECT 1 FROM deployments WHERE id=NEW.deployment_id AND environment_workload_runtime IS NOT NULL) THEN RETURN NEW; END IF;
 IF TG_OP='UPDATE' AND NOT EXISTS(SELECT 1 FROM deployments WHERE id=OLD.deployment_id AND environment_workload_runtime IS NOT NULL) THEN
  RAISE EXCEPTION 'environment qualification instances must be created under their attempt' USING ERRCODE='23514';
 END IF;
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
        AND coalesce(nullif(member->'queue_modes','null'::jsonb),'{}'::jsonb)='{}'::jsonb)))
  OR q.lease_until<=clock_timestamp() OR q.lease_token IS DISTINCT FROM current_setting('gregale.gitops_qualification',true)
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
-- +goose StatementEnd

CREATE TABLE IF NOT EXISTS environment_qualification_job_smoke_receipts (
 request_id uuid NOT NULL,
 attempt bigint NOT NULL CHECK(attempt>0),
 graph_id uuid NOT NULL REFERENCES environment_workload_graphs(id),
 instance_id uuid NOT NULL UNIQUE REFERENCES environment_qualification_executions(instance_id),
 resource text NOT NULL CHECK(resource ~ '^workload/[a-z0-9]([a-z0-9-]{1,38})[a-z0-9]$'),
 policy_id text NOT NULL CHECK(policy_id='job-exit-v2'),
 policy_sha256 text NOT NULL CHECK(policy_sha256 ~ '^[0-9a-f]{64}$'),
 result_sha256 text NOT NULL CHECK(result_sha256 ~ '^[0-9a-f]{64}$'),
 exit_code integer NOT NULL CHECK(exit_code=0),
 error_class text NOT NULL CHECK(error_class='succeeded'),
 signal integer NOT NULL CHECK(signal=0),
 recorded_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(request_id,attempt)
);

-- A job receipt is accepted only for the exact, current job attempt after the
-- attempt-bound native process has been durably retired. It is not an app-run
-- record and can never be used to settle customer tasks or output manifests.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_environment_qualification_job_smoke_receipt() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE q environment_workload_qualification_requests%ROWTYPE;
        g environment_workload_graphs%ROWTYPE;
        e environment_qualification_executions%ROWTYPE;
        i instances%ROWTYPE;
BEGIN
 IF TG_OP<>'INSERT' THEN
  RAISE EXCEPTION 'qualification job smoke evidence is immutable' USING ERRCODE='23514';
 END IF;
 SELECT * INTO q FROM environment_workload_qualification_requests WHERE id=NEW.request_id FOR UPDATE;
 SELECT * INTO g FROM environment_workload_graphs WHERE id=NEW.graph_id;
 SELECT * INTO e FROM environment_qualification_executions WHERE instance_id=NEW.instance_id;
 SELECT * INTO i FROM instances WHERE id=NEW.instance_id;
 IF q.id IS NULL OR q.phase<>'claimed' OR q.lease_until<=clock_timestamp() OR q.attempt<>NEW.attempt
  OR q.graph_id<>NEW.graph_id OR q.reserved_instance_id<>NEW.instance_id OR q.resource<>NEW.resource
  OR q.execution_mode<>'job' OR q.lease_token IS DISTINCT FROM current_setting('gregale.gitops_qualification',true)
  OR q.frozen_inputs->'job_smoke' IS NULL OR jsonb_typeof(q.frozen_inputs->'job_smoke')<>'object'
  OR coalesce(nullif(q.frozen_inputs->'queue_bindings','null'::jsonb),'{}'::jsonb)<>'{}'::jsonb
  OR NOT environment_workload_qualification_inputs_current(q.id)
  OR g.id IS NULL OR g.phase<>'prepared' OR g.revision_id::text IS DISTINCT FROM q.frozen_inputs->>'revision_id'
  OR g.generation::text IS DISTINCT FROM q.frozen_inputs->>'generation' OR g.plan_hash IS DISTINCT FROM q.frozen_inputs->>'plan_hash'
  OR NOT EXISTS(SELECT 1 FROM jsonb_array_elements(g.members) member
      WHERE member->>'resource'=q.resource AND member->>'app_id'=q.app_id::text
       AND member->>'candidate_deployment_id'=q.deployment_id::text AND member->>'execution_mode'='job'
       AND coalesce((member->>'job_smoke_configured')::boolean,false)
       AND coalesce(nullif(member->'service_bindings','null'::jsonb),'{}'::jsonb)=coalesce(nullif(q.frozen_inputs->'service_bindings','null'::jsonb),'{}'::jsonb)
       AND coalesce((member->>'service_bindings_configured')::boolean,false)=(coalesce(nullif(q.frozen_inputs->'service_bindings','null'::jsonb),'{}'::jsonb)<>'{}'::jsonb)
       AND coalesce(member->>'queue_bindings_configured','false')='false'
       AND coalesce(nullif(member->'queue_bindings','null'::jsonb),'{}'::jsonb)='{}'::jsonb
       AND coalesce(nullif(member->'queue_modes','null'::jsonb),'{}'::jsonb)='{}'::jsonb)
  OR e.instance_id IS NULL OR e.request_id IS DISTINCT FROM q.id OR e.frame->>'graph_id' IS DISTINCT FROM q.graph_id::text
  OR e.frame->>'attempt' IS DISTINCT FROM q.attempt::text OR e.frame->>'resource' IS DISTINCT FROM q.resource
  OR e.frame->'artifact' IS DISTINCT FROM q.artifact OR NOT e.dispatch_started OR e.retired_at IS NULL
  OR e.retirement->>'kind' IS DISTINCT FROM 'native_retired' OR e.retirement->'processes_exited' IS DISTINCT FROM 'true'::jsonb
  OR e.retirement->'resources_removed' IS DISTINCT FROM 'true'::jsonb
  OR NOT EXISTS(SELECT 1 FROM environment_qualification_config_receipts c
      WHERE c.request_id=q.id AND c.attempt=q.attempt AND c.graph_id=q.graph_id AND c.instance_id=NEW.instance_id
       AND c.capture_instance_id IS NULL AND c.api_env_sha256 ~ '^[0-9a-f]{64}$')
  OR i.id IS NULL OR i.state<>'stopped' OR i.wake_id IS DISTINCT FROM (e.frame->>'wake_id')::uuid THEN
  RAISE EXCEPTION 'job smoke receipt requires a successful sanitized result for the current retired Git-owned job attempt' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS environment_qualification_job_smoke_receipt_guard ON environment_qualification_job_smoke_receipts;
CREATE TRIGGER environment_qualification_job_smoke_receipt_guard
 BEFORE INSERT OR UPDATE OR DELETE ON environment_qualification_job_smoke_receipts
 FOR EACH ROW EXECUTE FUNCTION guard_environment_qualification_job_smoke_receipt();

-- +goose Down
-- Retain durable qualification evidence and its immutable guard on rollback.
SELECT 1;
