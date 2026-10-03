-- ADR-435: immutable attempt placement survives control-plane purge. Native
-- retirement evidence, not generic terminal state, releases its reservation.
-- +goose Up
CREATE TABLE IF NOT EXISTS environment_qualification_executions (
 instance_id uuid PRIMARY KEY CHECK(instance_id<>'00000000-0000-0000-0000-000000000000'),
 request_id uuid NOT NULL CHECK(request_id<>'00000000-0000-0000-0000-000000000000'),
 frame jsonb NOT NULL CHECK(jsonb_typeof(frame)='object'),
 cleanup_token uuid NOT NULL UNIQUE CHECK(cleanup_token<>'00000000-0000-0000-0000-000000000000'),
 dispatch_started boolean NOT NULL DEFAULT false,
 retirement jsonb CHECK(retirement IS NULL OR jsonb_typeof(retirement)='object'),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 retired_at timestamptz,
 CHECK((retirement IS NULL)=(retired_at IS NULL)),
 CONSTRAINT environment_qualification_native_receipt_canonical CHECK(retirement IS NULL OR retirement->>'kind'<>'native_retired' OR
  (retirement->>'receipt_id'=(retirement->>'receipt_id')::uuid::text AND
   retirement->>'kernel_boot_id'=(retirement->>'kernel_boot_id')::uuid::text AND
   retirement->>'native_generation'=(retirement->>'native_generation')::uuid::text))
);
-- No parent foreign keys: revocation/removal must not erase cleanup identity.
CREATE INDEX IF NOT EXISTS environment_qualification_execution_node_pending_idx ON environment_qualification_executions((frame->>'node_id')) WHERE retired_at IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS environment_qualification_native_receipt_unique_idx ON environment_qualification_executions((retirement->>'receipt_id')) WHERE retirement->>'kind'='native_retired';
CREATE UNIQUE INDEX IF NOT EXISTS environment_qualification_native_incarnation_unique_idx ON environment_qualification_executions((frame->>'node_id'),(retirement->>'kernel_boot_id'),(retirement->>'native_generation')) WHERE retirement->>'kind'='native_retired';

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION environment_qualification_execution_frame(q environment_workload_qualification_requests,i instances) RETURNS jsonb LANGUAGE sql IMMUTABLE AS $$
 SELECT jsonb_build_object('instance_id',i.id,'request_id',q.id,'graph_id',q.graph_id,'app_id',i.app_id,'deployment_id',i.deployment_id,
  'node_id',i.node_id,'wake_id',i.wake_id,'source_id',q.frozen_inputs->>'source_id','environment_id',q.frozen_inputs->>'environment_id',
  'revision_id',q.frozen_inputs->>'revision_id','resource',q.resource,'scope',q.frozen_inputs->>'scope','plan_hash',q.frozen_inputs->>'plan_hash',
  'generation',(q.frozen_inputs->>'generation')::bigint,'intent_version',(q.frozen_inputs->>'intent_version')::bigint,
  'attempt',q.attempt,'ram_mb',i.ram_mb,'artifact',q.artifact);
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_environment_qualification_execution() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE q environment_workload_qualification_requests%ROWTYPE; i instances%ROWTYPE;
BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'qualification execution retention requires explicit collection' USING ERRCODE='23514'; END IF;
 IF TG_OP='INSERT' THEN
  SELECT * INTO q FROM environment_workload_qualification_requests WHERE id=NEW.request_id;
  SELECT * INTO i FROM instances WHERE id=NEW.instance_id;
  IF q.id IS NULL OR i.id IS NULL OR q.reserved_instance_id IS DISTINCT FROM i.id OR q.app_id IS DISTINCT FROM i.app_id
   OR q.deployment_id IS DISTINCT FROM i.deployment_id OR NEW.frame IS DISTINCT FROM environment_qualification_execution_frame(q,i)
   OR NEW.retirement IS NOT NULL OR NEW.retired_at IS NOT NULL THEN
   RAISE EXCEPTION 'qualification execution requires its original admitted attempt' USING ERRCODE='23514';
  END IF;
  RETURN NEW;
 END IF;
 IF ROW(NEW.instance_id,NEW.request_id,NEW.frame,NEW.cleanup_token,NEW.created_at)
  IS DISTINCT FROM ROW(OLD.instance_id,OLD.request_id,OLD.frame,OLD.cleanup_token,OLD.created_at)
  OR OLD.dispatch_started AND NOT NEW.dispatch_started OR OLD.retired_at IS NOT NULL AND NEW IS DISTINCT FROM OLD THEN
  RAISE EXCEPTION 'qualification execution identity and retirement are immutable' USING ERRCODE='23514';
 END IF;
 IF NOT OLD.dispatch_started AND NEW.dispatch_started THEN
  SELECT * INTO q FROM environment_workload_qualification_requests WHERE id=NEW.request_id;
  IF OLD.retired_at IS NOT NULL OR q.id IS NULL OR q.phase<>'claimed' OR q.lease_until<=clock_timestamp()
   OR q.lease_token IS DISTINCT FROM current_setting('gregale.gitops_qualification',true)
   OR q.reserved_instance_id IS DISTINCT FROM NEW.instance_id OR q.attempt IS DISTINCT FROM (NEW.frame->>'attempt')::bigint
   OR NOT environment_workload_qualification_inputs_current(q.id) THEN
   RAISE EXCEPTION 'qualification dispatch requires its current execution lease' USING ERRCODE='23514';
  END IF;
 END IF;
 IF OLD.retired_at IS NULL AND NEW.retired_at IS NOT NULL THEN
  IF NEW.cleanup_token::text IS DISTINCT FROM current_setting('gregale.gitops_qualification_cleanup',true)
   OR NEW.dispatch_started IS DISTINCT FROM OLD.dispatch_started THEN
   RAISE EXCEPTION 'qualification retirement requires its original cleanup capability' USING ERRCODE='23514';
  END IF;
  IF NOT OLD.dispatch_started THEN
   IF NEW.retirement IS DISTINCT FROM '{"kind":"never_dispatched"}'::jsonb THEN
    RAISE EXCEPTION 'unstarted qualification requires exact no-dispatch evidence' USING ERRCODE='23514';
   END IF;
  ELSIF NEW.retirement->>'kind' IS DISTINCT FROM 'native_retired'
   OR NEW.retirement->'processes_exited' IS DISTINCT FROM 'true'::jsonb OR NEW.retirement->'resources_removed' IS DISTINCT FROM 'true'::jsonb
   OR coalesce((NEW.retirement->>'receipt_id')::uuid,'00000000-0000-0000-0000-000000000000')='00000000-0000-0000-0000-000000000000'
   OR coalesce((NEW.retirement->>'native_generation')::uuid,'00000000-0000-0000-0000-000000000000')='00000000-0000-0000-0000-000000000000'
   OR coalesce((NEW.retirement->>'kernel_boot_id')::uuid,'00000000-0000-0000-0000-000000000000')='00000000-0000-0000-0000-000000000000'
   OR NEW.retirement-(ARRAY['kind','receipt_id','native_generation','kernel_boot_id','processes_exited','resources_removed'])<>'{}'::jsonb THEN
   RAISE EXCEPTION 'qualification retirement requires complete native evidence' USING ERRCODE='23514';
  END IF;
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS guard_environment_qualification_execution ON environment_qualification_executions;
CREATE TRIGGER guard_environment_qualification_execution BEFORE INSERT OR UPDATE OR DELETE ON environment_qualification_executions
 FOR EACH ROW EXECUTE FUNCTION guard_environment_qualification_execution();

-- Preserve uncertain pre-upgrade admission as dispatched. Generic terminal
-- rows cannot retroactively prove that an earlier host effect never happened.
-- Replay preserves the original frame, cleanup token, dispatch and receipt.
INSERT INTO environment_qualification_executions(instance_id,request_id,frame,cleanup_token,dispatch_started)
 SELECT i.id,q.id,environment_qualification_execution_frame(q,i),gen_random_uuid(),true
 FROM instances i JOIN environment_workload_qualification_requests q ON q.reserved_instance_id=i.id
 ON CONFLICT(instance_id) DO NOTHING;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION capture_environment_qualification_execution() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE q environment_workload_qualification_requests%ROWTYPE;
BEGIN
 SELECT * INTO q FROM environment_workload_qualification_requests WHERE reserved_instance_id=NEW.id;
 IF q.id IS NOT NULL THEN
  INSERT INTO environment_qualification_executions(instance_id,request_id,frame,cleanup_token)
   VALUES(NEW.id,q.id,environment_qualification_execution_frame(q,NEW),gen_random_uuid());
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS capture_environment_qualification_execution ON instances;
CREATE TRIGGER capture_environment_qualification_execution AFTER INSERT ON instances FOR EACH ROW EXECUTE FUNCTION capture_environment_qualification_execution();

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_environment_qualification_physical_retirement() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE e environment_qualification_executions%ROWTYPE;
BEGIN
 SELECT * INTO e FROM environment_qualification_executions WHERE instance_id=OLD.id;
 IF e.instance_id IS NOT NULL THEN
  IF TG_OP='UPDATE' AND NEW.state='running' AND (NOT e.dispatch_started OR e.retired_at IS NOT NULL) THEN
   RAISE EXCEPTION 'qualification runtime requires a dispatched unretired attempt' USING ERRCODE='23514';
  END IF;
  IF TG_OP='DELETE' AND e.retired_at IS NULL OR TG_OP='UPDATE' AND NEW.state NOT IN ('cold_booting','waking','running','draining','warm') AND e.retired_at IS NULL THEN
   RAISE EXCEPTION 'qualification reservation requires attempt-bound physical retirement' USING ERRCODE='23514';
  END IF;
  IF TG_OP='UPDATE' AND ROW(NEW.id,NEW.app_id,NEW.deployment_id,NEW.node_id,NEW.wake_id,NEW.ram_mb)
   IS DISTINCT FROM ROW(OLD.id,OLD.app_id,OLD.deployment_id,OLD.node_id,OLD.wake_id,OLD.ram_mb) THEN
   RAISE EXCEPTION 'qualification placement survives parent removal' USING ERRCODE='23514';
  END IF;
 ELSIF EXISTS(SELECT 1 FROM deployments WHERE id=OLD.deployment_id AND environment_workload_runtime IS NOT NULL)
  AND (TG_OP='DELETE' OR NEW.state NOT IN ('cold_booting','waking','running','draining','warm')) THEN
  RAISE EXCEPTION 'legacy qualification has no recoverable execution frame' USING ERRCODE='23514';
 END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS guard_environment_qualification_physical_retirement ON instances;
CREATE TRIGGER guard_environment_qualification_physical_retirement BEFORE DELETE OR UPDATE ON instances
 FOR EACH ROW EXECUTE FUNCTION guard_environment_qualification_physical_retirement();

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_environment_qualification_attempt_replacement() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.reserved_instance_id IS DISTINCT FROM OLD.reserved_instance_id AND EXISTS(
  SELECT 1 FROM environment_qualification_executions WHERE instance_id=OLD.reserved_instance_id AND retired_at IS NULL) THEN
  RAISE EXCEPTION 'qualification attempt replacement requires physical retirement' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS guard_environment_qualification_attempt_replacement ON environment_workload_qualification_requests;
CREATE TRIGGER guard_environment_qualification_attempt_replacement BEFORE UPDATE ON environment_workload_qualification_requests
 FOR EACH ROW EXECUTE FUNCTION guard_environment_qualification_attempt_replacement();

-- +goose Down
-- Preserve physical retirement fences when rolling back binaries.
SELECT 1;
