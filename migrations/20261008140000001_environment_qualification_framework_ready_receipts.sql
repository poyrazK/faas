-- ADR-568: keep restored guest framework readiness separate from config and
-- application health smoke proofs.
-- +goose Up
CREATE TABLE IF NOT EXISTS environment_qualification_framework_ready_receipts (
 request_id uuid NOT NULL REFERENCES environment_workload_qualification_requests(id),
 attempt bigint NOT NULL CHECK(attempt>0),
 graph_id uuid NOT NULL REFERENCES environment_workload_graphs(id),
 capture_instance_id uuid NOT NULL REFERENCES environment_qualification_snapshot_receipts(instance_id),
 instance_id uuid NOT NULL UNIQUE REFERENCES environment_qualification_executions(instance_id),
 runtime text NOT NULL CHECK(runtime ~ '^[a-z0-9][a-z0-9._-]{0,31}$'),
 warmup_ms bigint NOT NULL CHECK(warmup_ms BETWEEN 0 AND 4294967295),
 recorded_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(request_id,attempt),
 CHECK(instance_id<>capture_instance_id)
);

-- This event is accepted only from a dispatched restored target while that
-- exact private execution is live. The framework-ready wire has no caller ID;
-- vmmd's private channel supplies the immutable execution identity.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_environment_qualification_framework_ready_receipt() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE q environment_workload_qualification_requests%ROWTYPE;
        graph environment_workload_graphs%ROWTYPE;
        target_exec environment_qualification_executions%ROWTYPE;
        target_instance instances%ROWTYPE;
        target_runtime instance_runtime_config_receipts%ROWTYPE;
        reservation environment_qualification_restore_reservations%ROWTYPE;
        capture environment_qualification_snapshot_receipts%ROWTYPE;
BEGIN
 IF TG_OP<>'INSERT' THEN
  RAISE EXCEPTION 'qualification framework-ready evidence is immutable' USING ERRCODE='23514';
 END IF;
 SELECT * INTO q FROM environment_workload_qualification_requests WHERE id=NEW.request_id FOR UPDATE;
 SELECT * INTO graph FROM environment_workload_graphs WHERE id=NEW.graph_id;
 SELECT * INTO target_exec FROM environment_qualification_executions WHERE instance_id=NEW.instance_id;
 SELECT * INTO target_instance FROM instances WHERE id=NEW.instance_id;
 SELECT * INTO target_runtime FROM instance_runtime_config_receipts WHERE instance_id=NEW.instance_id AND wake_id=target_instance.wake_id;
 SELECT * INTO reservation FROM environment_qualification_restore_reservations WHERE instance_id=NEW.instance_id;
 SELECT * INTO capture FROM environment_qualification_snapshot_receipts WHERE instance_id=NEW.capture_instance_id;
 IF q.id IS NULL OR q.phase<>'claimed' OR q.lease_until<=clock_timestamp() OR q.attempt<>NEW.attempt
  OR q.graph_id<>NEW.graph_id OR q.reserved_instance_id<>NEW.capture_instance_id
  OR coalesce(q.frozen_inputs->>'runtime_base','')<>'' AND NEW.runtime IS DISTINCT FROM q.frozen_inputs->>'runtime_base'
  OR q.lease_token IS DISTINCT FROM current_setting('gregale.gitops_qualification',true)
  OR NOT environment_workload_qualification_inputs_current(q.id)
  OR graph.id IS NULL OR graph.phase<>'prepared' OR graph.revision_id::text<>q.frozen_inputs->>'revision_id'
  OR graph.generation::text<>q.frozen_inputs->>'generation' OR graph.plan_hash<>q.frozen_inputs->>'plan_hash'
  OR NOT EXISTS(SELECT 1 FROM jsonb_array_elements(graph.members) member
      WHERE member->>'resource'=q.resource AND member->>'app_id'=q.app_id::text
       AND member->>'candidate_deployment_id'=q.deployment_id::text)
  OR target_exec.instance_id IS NULL OR target_exec.request_id<>q.id
  OR target_exec.capture_instance_id<>NEW.capture_instance_id OR NOT target_exec.dispatch_started OR target_exec.retired_at IS NOT NULL
  OR target_exec.frame->>'instance_id' IS DISTINCT FROM NEW.instance_id::text
  OR target_exec.frame->>'request_id' IS DISTINCT FROM q.id::text
  OR target_exec.frame->>'graph_id' IS DISTINCT FROM q.graph_id::text
  OR target_exec.frame->>'attempt' IS DISTINCT FROM q.attempt::text
  OR target_exec.frame->>'app_id' IS DISTINCT FROM q.app_id::text
  OR target_exec.frame->>'deployment_id' IS DISTINCT FROM q.deployment_id::text
  OR target_instance.id IS NULL OR target_instance.app_id<>q.app_id OR target_instance.deployment_id<>q.deployment_id
  OR target_instance.state<>'running' OR target_instance.wake_id IS DISTINCT FROM (target_exec.frame->>'wake_id')::uuid
  OR target_runtime.instance_id IS NULL OR target_runtime.wake_id<>target_instance.wake_id
  OR capture.instance_id IS NULL OR target_runtime.scope IS DISTINCT FROM capture.inputs->>'scope'
  OR target_runtime.variables IS DISTINCT FROM capture.inputs->'variables'
  OR target_runtime.secret_versions IS DISTINCT FROM capture.inputs->'secret_versions'
  OR target_runtime.secret_refs IS DISTINCT FROM capture.inputs->'secret_refs'
  OR coalesce(target_runtime.sidecar_secret_versions,'{}'::jsonb) IS DISTINCT FROM coalesce(capture.inputs->'sidecar_secret_versions','{}'::jsonb)
  OR target_runtime.all_secrets IS DISTINCT FROM (capture.inputs->>'all_secrets')::boolean
  OR reservation.instance_id IS NULL OR reservation.request_id<>q.id OR reservation.attempt<>q.attempt
  OR reservation.capture_instance_id<>NEW.capture_instance_id
  OR NOT environment_runtime_inputs_fresh(target_instance.app_id,target_runtime.scope,target_runtime.boundary_at,
      target_runtime.variables,target_runtime.secret_versions,target_runtime.all_secrets,target_runtime.secret_refs,
      target_runtime.sidecar_secret_versions) THEN
  RAISE EXCEPTION 'framework-ready receipt requires the current live restored graph target' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS environment_qualification_framework_ready_receipt_guard ON environment_qualification_framework_ready_receipts;
CREATE TRIGGER environment_qualification_framework_ready_receipt_guard
 BEFORE INSERT OR UPDATE OR DELETE ON environment_qualification_framework_ready_receipts
 FOR EACH ROW EXECUTE FUNCTION guard_environment_qualification_framework_ready_receipt();

-- +goose Down
-- Keep evidence and its immutable fence across binary rollback.
SELECT 1;
