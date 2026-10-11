-- ADR-568: retain successful native restore evidence after target cleanup.
-- +goose Up
CREATE TABLE IF NOT EXISTS environment_qualification_restore_receipts (
 request_id uuid NOT NULL,
 attempt bigint NOT NULL CHECK(attempt>0),
 capture_instance_id uuid NOT NULL REFERENCES environment_qualification_snapshot_receipts(instance_id),
 instance_id uuid NOT NULL UNIQUE REFERENCES environment_qualification_executions(instance_id),
 runtime_inputs jsonb NOT NULL CHECK(jsonb_typeof(runtime_inputs)='object'),
 recorded_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(request_id,attempt),
 CHECK(instance_id<>capture_instance_id)
);

-- The capture receipt ID is the incoming native generation. Retirement must
-- acknowledge that exact producer, in addition to its process generation and
-- kernel boot. Keep the SQL boundary aligned with the stores.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_environment_qualification_capture_retirement() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE capture jsonb;
BEGIN
 IF NEW.retired_at IS NOT NULL AND OLD.retired_at IS NULL THEN
  SELECT snapshot INTO capture FROM environment_qualification_snapshot_receipts WHERE instance_id=NEW.instance_id;
  IF capture IS NOT NULL AND (NEW.retirement->>'kind' IS DISTINCT FROM 'native_retired'
   OR NEW.retirement->>'receipt_id' IS DISTINCT FROM capture->>'capture_id'
   OR NEW.retirement->>'native_generation' IS DISTINCT FROM capture->>'native_generation'
   OR NEW.retirement->>'kernel_boot_id' IS DISTINCT FROM capture->>'kernel_boot_id') THEN
   RAISE EXCEPTION 'qualification retirement disagrees with original capture producer' USING ERRCODE='23514';
  END IF;
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd

-- A restore target must retire on the captured host kernel with its own
-- native generation and receipt. Reject borrowed or cross-boot proof early.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_environment_qualification_restore_retirement() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE capture environment_qualification_snapshot_receipts%ROWTYPE;
        original environment_qualification_executions%ROWTYPE;
BEGIN
 IF OLD.retired_at IS NULL AND NEW.retired_at IS NOT NULL AND NEW.capture_instance_id IS NOT NULL
  AND NEW.retirement->>'kind'='native_retired' THEN
  SELECT * INTO capture FROM environment_qualification_snapshot_receipts WHERE instance_id=NEW.capture_instance_id;
  SELECT * INTO original FROM environment_qualification_executions WHERE instance_id=NEW.capture_instance_id;
  IF capture.instance_id IS NULL OR original.retirement IS NULL
   OR NEW.retirement->>'native_generation' IS NOT DISTINCT FROM capture.snapshot->>'native_generation'
   OR NEW.retirement->>'kernel_boot_id' IS DISTINCT FROM capture.snapshot->>'kernel_boot_id'
   OR NEW.retirement->>'receipt_id' IS NOT DISTINCT FROM original.retirement->>'receipt_id' THEN
   RAISE EXCEPTION 'restore target requires distinct native retirement on the capture kernel' USING ERRCODE='23514';
  END IF;
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd

-- The target execution and its runtime receipt prove a successful dedicated
-- restore RPC. Preserve the input set and both identities after VM cleanup;
-- this receipt does not attest smoke, readiness, activation or serving.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_environment_qualification_restore_receipt() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE q environment_workload_qualification_requests%ROWTYPE;
        source_exec environment_qualification_executions%ROWTYPE;
        target_exec environment_qualification_executions%ROWTYPE;
        capture environment_qualification_snapshot_receipts%ROWTYPE;
        runtime instance_runtime_config_receipts%ROWTYPE;
        target_instance instances%ROWTYPE;
        reservation environment_qualification_restore_reservations%ROWTYPE;
BEGIN
 IF TG_OP<>'INSERT' THEN
  RAISE EXCEPTION 'qualification restore evidence is immutable' USING ERRCODE='23514';
 END IF;
 SELECT * INTO q FROM environment_workload_qualification_requests WHERE id=NEW.request_id FOR UPDATE;
 SELECT * INTO source_exec FROM environment_qualification_executions WHERE instance_id=NEW.capture_instance_id;
 SELECT * INTO capture FROM environment_qualification_snapshot_receipts WHERE instance_id=NEW.capture_instance_id;
 SELECT * INTO target_exec FROM environment_qualification_executions WHERE instance_id=NEW.instance_id;
 SELECT * INTO reservation FROM environment_qualification_restore_reservations WHERE instance_id=NEW.instance_id;
 SELECT * INTO target_instance FROM instances WHERE id=NEW.instance_id;
 SELECT * INTO runtime FROM instance_runtime_config_receipts WHERE instance_id=NEW.instance_id AND wake_id=target_instance.wake_id;
 IF q.id IS NULL OR q.phase<>'claimed' OR q.attempt<>NEW.attempt OR q.reserved_instance_id<>NEW.capture_instance_id
  OR q.lease_token IS DISTINCT FROM current_setting('gregale.gitops_qualification',true)
  OR NOT environment_workload_qualification_inputs_current(q.id)
  OR capture.instance_id IS NULL OR source_exec.instance_id IS NULL OR target_exec.instance_id IS NULL
  OR reservation.instance_id IS NULL OR reservation.request_id<>q.id OR reservation.attempt<>q.attempt
  OR reservation.capture_instance_id<>NEW.capture_instance_id
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
  OR target_exec.frame->>'instance_id' IS NOT DISTINCT FROM source_exec.frame->>'instance_id'
  OR target_exec.frame->>'wake_id' IS NOT DISTINCT FROM source_exec.frame->>'wake_id'
  OR (target_exec.frame-ARRAY['instance_id','wake_id','capture_instance_id'])
    IS DISTINCT FROM (source_exec.frame-ARRAY['instance_id','wake_id'])
  OR NEW.runtime_inputs->>'scope' IS DISTINCT FROM capture.inputs->>'scope'
  OR NEW.runtime_inputs->'variables' IS DISTINCT FROM capture.inputs->'variables'
  OR NEW.runtime_inputs->'secret_versions' IS DISTINCT FROM capture.inputs->'secret_versions'
  OR NEW.runtime_inputs->'secret_refs' IS DISTINCT FROM capture.inputs->'secret_refs'
  OR coalesce(NEW.runtime_inputs->'sidecar_secret_versions','{}'::jsonb) IS DISTINCT FROM coalesce(capture.inputs->'sidecar_secret_versions','{}'::jsonb)
  OR NEW.runtime_inputs->>'all_secrets' IS DISTINCT FROM capture.inputs->>'all_secrets'
  OR runtime.instance_id IS NULL OR target_instance.app_id<>q.app_id OR target_instance.deployment_id<>q.deployment_id
  OR target_instance.node_id IS DISTINCT FROM (source_exec.frame->>'node_id')::uuid
  OR target_instance.wake_id IS DISTINCT FROM (target_exec.frame->>'wake_id')::uuid OR target_instance.state<>'stopped'
  OR runtime.scope IS DISTINCT FROM NEW.runtime_inputs->>'scope'
  OR runtime.boundary_at IS DISTINCT FROM (NEW.runtime_inputs->>'boundary')::timestamptz
  OR runtime.variables IS DISTINCT FROM NEW.runtime_inputs->'variables'
  OR runtime.secret_versions IS DISTINCT FROM NEW.runtime_inputs->'secret_versions'
  OR runtime.secret_refs IS DISTINCT FROM NEW.runtime_inputs->'secret_refs'
  OR runtime.sidecar_secret_versions IS DISTINCT FROM coalesce(NEW.runtime_inputs->'sidecar_secret_versions','{}'::jsonb)
  OR runtime.all_secrets IS DISTINCT FROM (NEW.runtime_inputs->>'all_secrets')::boolean
  OR NEW.runtime_inputs-ARRAY['scope','boundary','variables','secret_versions','secret_refs','sidecar_secret_versions','all_secrets']<>'{}'::jsonb
  OR NOT environment_runtime_inputs_fresh(target_instance.app_id,runtime.scope,runtime.boundary_at,runtime.variables,
      runtime.secret_versions,runtime.all_secrets,runtime.secret_refs,runtime.sidecar_secret_versions)
  OR NOT environment_runtime_inputs_fresh(target_instance.app_id,capture.inputs->>'scope',(capture.inputs->>'boundary')::timestamptz,
      capture.inputs->'variables',capture.inputs->'secret_versions',(capture.inputs->>'all_secrets')::boolean,
      capture.inputs->'secret_refs',coalesce(capture.inputs->'sidecar_secret_versions','{}'::jsonb)) THEN
  RAISE EXCEPTION 'restore receipt requires the current capture, successful distinct restore, and fresh target inputs' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS environment_qualification_restore_receipt_guard ON environment_qualification_restore_receipts;
CREATE TRIGGER environment_qualification_restore_receipt_guard
 BEFORE INSERT OR UPDATE OR DELETE ON environment_qualification_restore_receipts
 FOR EACH ROW EXECUTE FUNCTION guard_environment_qualification_restore_receipt();

-- +goose Down
-- Keep evidence and its fence across binary rollback.
SELECT 1;
