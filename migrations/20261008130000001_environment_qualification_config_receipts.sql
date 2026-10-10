-- ADR-568: retain guest acknowledgement for the exact private API environment.
-- +goose Up
CREATE TABLE IF NOT EXISTS environment_qualification_config_receipts (
 request_id uuid NOT NULL REFERENCES environment_workload_qualification_requests(id),
 attempt bigint NOT NULL CHECK(attempt>0),
 graph_id uuid NOT NULL REFERENCES environment_workload_graphs(id),
 instance_id uuid NOT NULL UNIQUE REFERENCES environment_qualification_executions(instance_id),
 capture_instance_id uuid REFERENCES environment_qualification_executions(instance_id),
 api_env_sha256 text NOT NULL CHECK(api_env_sha256 ~ '^[0-9a-f]{64}$'),
 recorded_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(request_id,attempt,instance_id)
);

-- The manager waits for the guest's one-time, HMAC-bound config receipt
-- before returning success. Preserve only the non-secret API-env digest and
-- fence direct SQL writes to the current dispatched, published graph attempt.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_environment_qualification_config_receipt() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE q environment_workload_qualification_requests%ROWTYPE;
        e environment_qualification_executions%ROWTYPE;
        i instances%ROWTYPE;
        d deployments%ROWTYPE;
        runtime instance_runtime_config_receipts%ROWTYPE;
        reservation environment_qualification_restore_reservations%ROWTYPE;
BEGIN
 IF TG_OP<>'INSERT' THEN
  RAISE EXCEPTION 'qualification guest config evidence is immutable' USING ERRCODE='23514';
 END IF;
 SELECT * INTO q FROM environment_workload_qualification_requests WHERE id=NEW.request_id FOR UPDATE;
 SELECT * INTO e FROM environment_qualification_executions WHERE instance_id=NEW.instance_id;
 SELECT * INTO i FROM instances WHERE id=NEW.instance_id;
 SELECT * INTO d FROM deployments WHERE id=i.deployment_id;
 SELECT * INTO runtime FROM instance_runtime_config_receipts WHERE instance_id=NEW.instance_id AND wake_id=i.wake_id;
 IF e.capture_instance_id IS NOT NULL THEN
  SELECT * INTO reservation FROM environment_qualification_restore_reservations WHERE instance_id=NEW.instance_id;
 END IF;
 IF q.id IS NULL OR q.phase<>'claimed' OR q.attempt<>NEW.attempt OR q.graph_id<>NEW.graph_id
  OR q.lease_until<=clock_timestamp() OR q.lease_token IS DISTINCT FROM current_setting('gregale.gitops_qualification',true)
  OR NOT environment_workload_qualification_inputs_current(q.id)
  OR e.instance_id IS NULL OR e.request_id<>q.id OR e.capture_instance_id IS DISTINCT FROM NEW.capture_instance_id
  OR NOT e.dispatch_started OR e.retired_at IS NOT NULL
  OR e.frame->>'instance_id' IS DISTINCT FROM i.id::text OR e.frame->>'request_id' IS DISTINCT FROM q.id::text
  OR e.frame->>'graph_id' IS DISTINCT FROM q.graph_id::text OR e.frame->>'attempt' IS DISTINCT FROM q.attempt::text
  OR e.frame->>'app_id' IS DISTINCT FROM q.app_id::text OR e.frame->>'deployment_id' IS DISTINCT FROM q.deployment_id::text
  OR i.app_id<>q.app_id OR i.deployment_id<>q.deployment_id OR i.state<>'running'
  OR i.wake_id IS DISTINCT FROM (e.frame->>'wake_id')::uuid OR d.environment_workload_runtime IS DISTINCT FROM q.frozen_inputs
  OR runtime.instance_id IS NULL OR runtime.wake_id<>i.wake_id OR runtime.scope IS DISTINCT FROM q.frozen_inputs->>'scope'
  OR NOT environment_runtime_inputs_fresh(i.app_id,runtime.scope,runtime.boundary_at,runtime.variables,
      runtime.secret_versions,runtime.all_secrets,runtime.secret_refs,runtime.sidecar_secret_versions)
  OR NEW.capture_instance_id IS NULL AND i.id<>q.reserved_instance_id
  OR NEW.capture_instance_id IS NOT NULL AND (reservation.instance_id IS NULL OR reservation.request_id<>q.id
      OR reservation.attempt<>q.attempt OR reservation.capture_instance_id<>q.reserved_instance_id)
 THEN
  RAISE EXCEPTION 'guest config receipt requires the current acknowledged private graph runtime' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS environment_qualification_config_receipt_guard ON environment_qualification_config_receipts;
CREATE TRIGGER environment_qualification_config_receipt_guard
 BEFORE INSERT OR UPDATE OR DELETE ON environment_qualification_config_receipts
 FOR EACH ROW EXECUTE FUNCTION guard_environment_qualification_config_receipt();

-- Successful restore evidence also requires both private runtimes to have
-- acknowledged their exact guest configuration. Keep this fence at the SQL
-- boundary as well as in the stores so direct writes cannot bypass it.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION require_environment_qualification_restore_guest_config() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE source_config environment_qualification_config_receipts%ROWTYPE;
        target_config environment_qualification_config_receipts%ROWTYPE;
BEGIN
 IF TG_OP<>'INSERT' THEN
  RAISE EXCEPTION 'qualification restore guest config evidence is immutable' USING ERRCODE='23514';
 END IF;
 SELECT * INTO source_config FROM environment_qualification_config_receipts
  WHERE request_id=NEW.request_id AND attempt=NEW.attempt AND instance_id=NEW.capture_instance_id
   AND capture_instance_id IS NULL;
 SELECT * INTO target_config FROM environment_qualification_config_receipts
  WHERE request_id=NEW.request_id AND attempt=NEW.attempt AND instance_id=NEW.instance_id
   AND capture_instance_id=NEW.capture_instance_id;
 IF source_config.instance_id IS NULL OR target_config.instance_id IS NULL
  OR source_config.graph_id IS DISTINCT FROM target_config.graph_id THEN
  RAISE EXCEPTION 'restore receipt requires acknowledged source and restored guest configuration' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS environment_qualification_restore_guest_config_guard ON environment_qualification_restore_receipts;
CREATE TRIGGER environment_qualification_restore_guest_config_guard
 BEFORE INSERT OR UPDATE OR DELETE ON environment_qualification_restore_receipts
 FOR EACH ROW EXECUTE FUNCTION require_environment_qualification_restore_guest_config();

-- +goose Down
-- Retain evidence and its immutable fence across binary rollback.
SELECT 1;
