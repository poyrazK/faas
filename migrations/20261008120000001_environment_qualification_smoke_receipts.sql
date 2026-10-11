-- ADR-568: persist isolated smoke success only after a complete restored graph visit.
-- +goose Up
CREATE TABLE IF NOT EXISTS environment_qualification_smoke_receipts (
 request_id uuid NOT NULL,
 attempt bigint NOT NULL CHECK(attempt>0),
 graph_id uuid NOT NULL REFERENCES environment_workload_graphs(id),
 capture_instance_id uuid NOT NULL REFERENCES environment_qualification_snapshot_receipts(instance_id),
 instance_id uuid NOT NULL UNIQUE REFERENCES environment_qualification_executions(instance_id),
 resource text NOT NULL CHECK(resource ~ '^workload/[a-z0-9]([a-z0-9-]{1,38})[a-z0-9]$'),
 policy_id text NOT NULL CHECK(policy_id ~ '^[a-z][a-z0-9._-]{0,63}$'),
 policy_sha256 text NOT NULL CHECK(policy_sha256 ~ '^[0-9a-f]{64}$'),
 result_sha256 text NOT NULL CHECK(result_sha256 ~ '^[0-9a-f]{64}$'),
 recorded_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(request_id,attempt),
 CHECK(instance_id<>capture_instance_id)
);

-- A smoke receipt is an immutable assertion emitted by schedd after its whole
-- restored graph visitor succeeded. Bind it to the current request lease and
-- the exact capture/restore pair; never accept a live or stale target.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_environment_qualification_smoke_receipt() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE q environment_workload_qualification_requests%ROWTYPE;
        graph environment_workload_graphs%ROWTYPE;
        capture environment_qualification_snapshot_receipts%ROWTYPE;
        source_exec environment_qualification_executions%ROWTYPE;
        target_exec environment_qualification_executions%ROWTYPE;
        restored environment_qualification_restore_receipts%ROWTYPE;
        target_instance instances%ROWTYPE;
BEGIN
 IF TG_OP<>'INSERT' THEN
  RAISE EXCEPTION 'qualification smoke evidence is immutable' USING ERRCODE='23514';
 END IF;
 SELECT * INTO q FROM environment_workload_qualification_requests WHERE id=NEW.request_id FOR UPDATE;
 SELECT * INTO graph FROM environment_workload_graphs WHERE id=NEW.graph_id;
 SELECT * INTO capture FROM environment_qualification_snapshot_receipts WHERE instance_id=NEW.capture_instance_id;
 SELECT * INTO source_exec FROM environment_qualification_executions WHERE instance_id=NEW.capture_instance_id;
 SELECT * INTO target_exec FROM environment_qualification_executions WHERE instance_id=NEW.instance_id;
 SELECT * INTO restored FROM environment_qualification_restore_receipts WHERE request_id=NEW.request_id AND attempt=NEW.attempt;
 SELECT * INTO target_instance FROM instances WHERE id=NEW.instance_id;
 IF q.id IS NULL OR q.phase<>'claimed' OR q.lease_until<=clock_timestamp() OR q.attempt<>NEW.attempt
  OR q.graph_id<>NEW.graph_id OR q.reserved_instance_id<>NEW.capture_instance_id OR q.resource<>NEW.resource
  OR q.lease_token IS DISTINCT FROM current_setting('gregale.gitops_qualification',true)
  OR NOT environment_workload_qualification_inputs_current(q.id)
  OR graph.id IS NULL OR graph.phase<>'prepared' OR graph.revision_id::text<>q.frozen_inputs->>'revision_id'
  OR graph.generation::text<>q.frozen_inputs->>'generation' OR graph.plan_hash<>q.frozen_inputs->>'plan_hash'
  OR NOT EXISTS(SELECT 1 FROM jsonb_array_elements(graph.members) member
      WHERE member->>'resource'=q.resource AND member->>'app_id'=q.app_id::text
       AND member->>'candidate_deployment_id'=q.deployment_id::text)
  OR capture.instance_id IS NULL OR source_exec.instance_id IS NULL OR target_exec.instance_id IS NULL
  OR restored.request_id IS NULL OR restored.capture_instance_id<>NEW.capture_instance_id OR restored.instance_id<>NEW.instance_id
  OR source_exec.request_id<>q.id OR source_exec.capture_instance_id IS NOT NULL OR NOT source_exec.dispatch_started
  OR source_exec.retired_at IS NULL OR source_exec.retirement->>'kind'<>'native_retired'
  OR source_exec.retirement->>'receipt_id' IS DISTINCT FROM capture.snapshot->>'capture_id'
  OR source_exec.retirement->>'native_generation' IS DISTINCT FROM capture.snapshot->>'native_generation'
  OR source_exec.retirement->>'kernel_boot_id' IS DISTINCT FROM capture.snapshot->>'kernel_boot_id'
  OR target_exec.request_id<>q.id OR target_exec.capture_instance_id<>NEW.capture_instance_id
  OR NOT target_exec.dispatch_started OR target_exec.retired_at IS NULL OR target_exec.retirement->>'kind'<>'native_retired'
  OR target_exec.retirement->'processes_exited' IS DISTINCT FROM 'true'::jsonb
  OR target_exec.retirement->'resources_removed' IS DISTINCT FROM 'true'::jsonb
  OR target_exec.retirement->>'native_generation' IS NOT DISTINCT FROM capture.snapshot->>'native_generation'
  OR target_exec.retirement->>'kernel_boot_id' IS DISTINCT FROM capture.snapshot->>'kernel_boot_id'
  OR target_exec.retirement->>'receipt_id' IS NOT DISTINCT FROM source_exec.retirement->>'receipt_id'
  OR restored.runtime_inputs->>'scope' IS DISTINCT FROM capture.inputs->>'scope'
  OR restored.runtime_inputs->'variables' IS DISTINCT FROM capture.inputs->'variables'
  OR restored.runtime_inputs->'secret_versions' IS DISTINCT FROM capture.inputs->'secret_versions'
  OR restored.runtime_inputs->'secret_refs' IS DISTINCT FROM capture.inputs->'secret_refs'
  OR coalesce(restored.runtime_inputs->'sidecar_secret_versions','{}'::jsonb) IS DISTINCT FROM coalesce(capture.inputs->'sidecar_secret_versions','{}'::jsonb)
  OR restored.runtime_inputs->>'all_secrets' IS DISTINCT FROM capture.inputs->>'all_secrets'
  OR target_instance.id IS NULL OR target_instance.state<>'stopped' OR target_instance.wake_id IS DISTINCT FROM (target_exec.frame->>'wake_id')::uuid
  OR NOT environment_runtime_inputs_fresh(target_instance.app_id,restored.runtime_inputs->>'scope',
      (restored.runtime_inputs->>'boundary')::timestamptz,restored.runtime_inputs->'variables',restored.runtime_inputs->'secret_versions',
      (restored.runtime_inputs->>'all_secrets')::boolean,restored.runtime_inputs->'secret_refs',
      coalesce(restored.runtime_inputs->'sidecar_secret_versions','{}'::jsonb)) THEN
  RAISE EXCEPTION 'smoke receipt requires explicit policy/result digests for the current graph member and its fresh, retired restore target' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS environment_qualification_smoke_receipt_guard ON environment_qualification_smoke_receipts;
CREATE TRIGGER environment_qualification_smoke_receipt_guard
 BEFORE INSERT OR UPDATE OR DELETE ON environment_qualification_smoke_receipts
 FOR EACH ROW EXECUTE FUNCTION guard_environment_qualification_smoke_receipt();

-- +goose Down
-- Keep evidence and its fence across binary rollback.
SELECT 1;
