-- ADR-568: immutable capture evidence, separate from serving snapshots.
-- +goose Up
CREATE TABLE environment_qualification_snapshot_receipts (
 instance_id uuid PRIMARY KEY REFERENCES environment_qualification_executions(instance_id),
 snapshot jsonb NOT NULL CHECK(jsonb_typeof(snapshot)='object'),
 inputs jsonb NOT NULL CHECK(jsonb_typeof(inputs)='object'),
 recorded_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
-- +goose StatementBegin
CREATE FUNCTION guard_environment_qualification_snapshot_receipt() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE e environment_qualification_executions%ROWTYPE; q environment_workload_qualification_requests%ROWTYPE;
 capture uuid; native uuid; kernel uuid; mem text;
BEGIN
 IF TG_OP<>'INSERT' THEN RAISE EXCEPTION 'qualification capture evidence is immutable' USING ERRCODE='23514'; END IF;
 SELECT * INTO e FROM environment_qualification_executions WHERE instance_id=NEW.instance_id FOR UPDATE;
 SELECT * INTO q FROM environment_workload_qualification_requests WHERE id=e.request_id;
 IF e.instance_id IS NULL OR NOT e.dispatch_started OR e.retired_at IS NOT NULL OR q.phase IS DISTINCT FROM 'claimed'
  OR q.lease_until<=clock_timestamp() OR q.lease_token IS DISTINCT FROM current_setting('gregale.gitops_qualification',true)
  OR q.reserved_instance_id IS DISTINCT FROM e.instance_id OR q.attempt IS DISTINCT FROM (e.frame->>'attempt')::bigint
  OR NOT environment_workload_qualification_inputs_current(q.id) THEN
  RAISE EXCEPTION 'qualification capture requires current original execution authority' USING ERRCODE='23514';
 END IF;
 capture:=(NEW.snapshot->>'capture_id')::uuid; native:=(NEW.snapshot->>'native_generation')::uuid; kernel:=(NEW.snapshot->>'kernel_boot_id')::uuid;
 mem:='snap/'||(e.frame->>'deployment_id')||'/warm/captures/'||capture::text||'/v2/mem';
 IF capture IS NULL OR native IS NULL OR kernel IS NULL OR capture=native
  OR NEW.snapshot->>'capture_id' IS DISTINCT FROM capture::text OR NEW.snapshot->>'native_generation' IS DISTINCT FROM native::text OR NEW.snapshot->>'kernel_boot_id' IS DISTINCT FROM kernel::text
  OR capture::text='00000000-0000-0000-0000-000000000000' OR native::text='00000000-0000-0000-0000-000000000000' OR kernel::text='00000000-0000-0000-0000-000000000000'
  OR NEW.snapshot->>'storage_key' IS DISTINCT FROM mem OR NEW.snapshot->>'vmstate_storage_key' IS DISTINCT FROM replace(mem,'/mem','/vmstate')
  OR NEW.snapshot->>'drive_storage_key' IS DISTINCT FROM replace(mem,'/mem','/drive') OR NEW.snapshot->>'backing_storage_key' IS DISTINCT FROM replace(mem,'/mem','/backing')
  OR NOT NEW.snapshot ?& ARRAY['mem_bytes','vmstate_bytes','stored_bytes'] OR coalesce((NEW.snapshot->>'mem_bytes')::bigint,0)<=0
  OR coalesce((NEW.snapshot->>'vmstate_bytes')::bigint,0)<=0 OR coalesce((NEW.snapshot->>'stored_bytes')::bigint,0)<=0 THEN
  RAISE EXCEPTION 'qualification capture namespace or outputs are invalid' USING ERRCODE='23514';
 END IF;
 IF NOT EXISTS(SELECT 1 FROM instances i JOIN instance_runtime_config_receipts c ON c.instance_id=i.id AND c.wake_id=i.wake_id
  WHERE i.id=e.instance_id AND i.state='running' AND i.node_id=(e.frame->>'node_id')::uuid AND i.wake_id=(e.frame->>'wake_id')::uuid
   AND c.scope=NEW.inputs->>'scope' AND c.boundary_at=(NEW.inputs->>'boundary')::timestamptz
   AND c.variables=NEW.inputs->'variables' AND c.secret_versions=NEW.inputs->'secret_versions' AND c.secret_refs=NEW.inputs->'secret_refs'
   AND c.sidecar_secret_versions=coalesce(NEW.inputs->'sidecar_secret_versions','{}') AND c.all_secrets=(NEW.inputs->>'all_secrets')::boolean
   AND environment_runtime_inputs_fresh(i.app_id,c.scope,c.boundary_at,c.variables,c.secret_versions,c.all_secrets,c.secret_refs,c.sidecar_secret_versions)) THEN
  RAISE EXCEPTION 'qualification capture requires fresh runtime input evidence' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER environment_qualification_snapshot_receipt_guard BEFORE INSERT OR UPDATE OR DELETE ON environment_qualification_snapshot_receipts
FOR EACH ROW EXECUTE FUNCTION guard_environment_qualification_snapshot_receipt();
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION guard_environment_qualification_capture_retirement() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE capture jsonb;
BEGIN
 IF NEW.retired_at IS NOT NULL AND OLD.retired_at IS NULL THEN
  SELECT snapshot INTO capture FROM environment_qualification_snapshot_receipts WHERE instance_id=NEW.instance_id;
  IF capture IS NOT NULL AND (NEW.retirement->>'kind' IS DISTINCT FROM 'native_retired'
   OR NEW.retirement->>'native_generation' IS DISTINCT FROM capture->>'native_generation'
   OR NEW.retirement->>'kernel_boot_id' IS DISTINCT FROM capture->>'kernel_boot_id') THEN
   RAISE EXCEPTION 'qualification retirement disagrees with original capture producer' USING ERRCODE='23514';
  END IF;
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER environment_qualification_capture_retirement_guard BEFORE UPDATE ON environment_qualification_executions
FOR EACH ROW EXECUTE FUNCTION guard_environment_qualification_capture_retirement();
-- +goose StatementEnd
-- +goose Down
DROP TRIGGER environment_qualification_capture_retirement_guard ON environment_qualification_executions;
DROP FUNCTION guard_environment_qualification_capture_retirement();

DROP TABLE environment_qualification_snapshot_receipts;
DROP FUNCTION guard_environment_qualification_snapshot_receipt();
