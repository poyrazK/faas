-- ADR-568: a separately charged restore target retains its original cohort.
-- +goose Up
CREATE TABLE IF NOT EXISTS environment_qualification_restore_reservations (
 instance_id uuid PRIMARY KEY CHECK(instance_id<>'00000000-0000-0000-0000-000000000000'),
 capture_instance_id uuid NOT NULL REFERENCES environment_qualification_snapshot_receipts(instance_id),
 request_id uuid NOT NULL,
 attempt bigint NOT NULL CHECK(attempt>0),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(request_id,attempt), CHECK(instance_id<>capture_instance_id)
);
ALTER TABLE environment_qualification_executions ADD COLUMN IF NOT EXISTS capture_instance_id uuid REFERENCES environment_qualification_snapshot_receipts(instance_id);

-- No source/request foreign key: cleanup identity outlives control-plane purge.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION environment_qualification_restore_current(request uuid,capture uuid) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT EXISTS(SELECT 1 FROM environment_workload_qualification_requests q
  JOIN environment_qualification_executions e ON e.instance_id=capture
  JOIN environment_qualification_snapshot_receipts r ON r.instance_id=e.instance_id
  WHERE q.id=request AND q.reserved_instance_id=capture AND q.phase='claimed' AND q.execution_mode<>'job'
   AND q.lease_until>clock_timestamp() AND q.lease_token=current_setting('gregale.gitops_qualification',true)
   AND e.request_id=q.id AND (e.frame->>'attempt')::bigint=q.attempt AND e.capture_instance_id IS NULL
   AND e.dispatch_started AND e.retired_at IS NOT NULL AND e.retirement->>'kind'='native_retired'
   AND e.retirement->>'native_generation'=r.snapshot->>'native_generation' AND e.retirement->>'kernel_boot_id'=r.snapshot->>'kernel_boot_id'
   AND e.frame->'artifact'=q.artifact AND e.frame->>'app_id'=q.app_id::text AND e.frame->>'deployment_id'=q.deployment_id::text
   AND environment_workload_qualification_inputs_current(q.id)
   AND environment_runtime_inputs_fresh(q.app_id,r.inputs->>'scope',(r.inputs->>'boundary')::timestamptz,
    r.inputs->'variables',r.inputs->'secret_versions',(r.inputs->>'all_secrets')::boolean,r.inputs->'secret_refs',coalesce(r.inputs->'sidecar_secret_versions','{}')));
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_environment_qualification_restore_reservation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP<>'INSERT' OR NOT environment_qualification_restore_current(NEW.request_id,NEW.capture_instance_id)
  OR NOT EXISTS(SELECT 1 FROM environment_workload_qualification_requests WHERE id=NEW.request_id AND attempt=NEW.attempt)
  OR EXISTS(SELECT 1 FROM instances WHERE id=NEW.instance_id) OR EXISTS(SELECT 1 FROM environment_qualification_executions WHERE instance_id=NEW.instance_id) THEN
  RAISE EXCEPTION 'restore reservation requires its current retired original capture' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS environment_qualification_restore_reservation_guard ON environment_qualification_restore_reservations;
CREATE TRIGGER environment_qualification_restore_reservation_guard BEFORE INSERT OR UPDATE OR DELETE ON environment_qualification_restore_reservations
 FOR EACH ROW EXECUTE FUNCTION guard_environment_qualification_restore_reservation();
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_environment_qualification_execution() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE q environment_workload_qualification_requests%ROWTYPE; i instances%ROWTYPE; b environment_qualification_restore_reservations%ROWTYPE;
BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'qualification execution retention requires explicit collection' USING ERRCODE='23514'; END IF;
 IF TG_OP='INSERT' THEN
  SELECT * INTO q FROM environment_workload_qualification_requests WHERE id=NEW.request_id;
  SELECT * INTO i FROM instances WHERE id=NEW.instance_id;
  SELECT * INTO b FROM environment_qualification_restore_reservations WHERE instance_id=NEW.instance_id;
  IF q.id IS NULL OR i.id IS NULL OR (q.reserved_instance_id IS DISTINCT FROM i.id AND (b.instance_id IS NULL OR b.request_id IS DISTINCT FROM q.id
    OR b.attempt IS DISTINCT FROM q.attempt OR NOT environment_qualification_restore_current(q.id,b.capture_instance_id)))
   OR NEW.capture_instance_id IS DISTINCT FROM b.capture_instance_id OR q.app_id IS DISTINCT FROM i.app_id
   OR q.deployment_id IS DISTINCT FROM i.deployment_id OR NEW.frame IS DISTINCT FROM (environment_qualification_execution_frame(q,i)||CASE WHEN b.capture_instance_id IS NULL THEN '{}'::jsonb ELSE jsonb_build_object('capture_instance_id',b.capture_instance_id) END)
   OR NEW.retirement IS NOT NULL OR NEW.retired_at IS NOT NULL THEN
   RAISE EXCEPTION 'qualification execution requires its original admitted attempt' USING ERRCODE='23514';
  END IF;
  RETURN NEW;
 END IF;
 IF ROW(NEW.instance_id,NEW.request_id,NEW.frame,NEW.cleanup_token,NEW.created_at,NEW.capture_instance_id)
  IS DISTINCT FROM ROW(OLD.instance_id,OLD.request_id,OLD.frame,OLD.cleanup_token,OLD.created_at,OLD.capture_instance_id)
  OR OLD.dispatch_started AND NOT NEW.dispatch_started OR OLD.retired_at IS NOT NULL AND NEW IS DISTINCT FROM OLD THEN
  RAISE EXCEPTION 'qualification execution identity and retirement are immutable' USING ERRCODE='23514';
 END IF;
 IF NOT OLD.dispatch_started AND NEW.dispatch_started THEN
  SELECT * INTO q FROM environment_workload_qualification_requests WHERE id=NEW.request_id;
  IF OLD.retired_at IS NOT NULL OR q.id IS NULL OR q.phase<>'claimed' OR q.lease_until<=clock_timestamp()
   OR q.lease_token IS DISTINCT FROM current_setting('gregale.gitops_qualification',true)
   OR (NEW.capture_instance_id IS NULL AND q.reserved_instance_id IS DISTINCT FROM NEW.instance_id)
   OR (NEW.capture_instance_id IS NOT NULL AND NOT environment_qualification_restore_current(q.id,NEW.capture_instance_id)) OR q.attempt IS DISTINCT FROM (NEW.frame->>'attempt')::bigint
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

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION capture_environment_qualification_execution() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE q environment_workload_qualification_requests%ROWTYPE; b environment_qualification_restore_reservations%ROWTYPE;
BEGIN
 SELECT * INTO q FROM environment_workload_qualification_requests WHERE reserved_instance_id=NEW.id;
 IF q.id IS NULL THEN
  SELECT * INTO b FROM environment_qualification_restore_reservations WHERE instance_id=NEW.id;
  SELECT * INTO q FROM environment_workload_qualification_requests WHERE id=b.request_id AND reserved_instance_id=b.capture_instance_id AND attempt=b.attempt;
 END IF;
 IF q.id IS NOT NULL THEN
  INSERT INTO environment_qualification_executions(instance_id,request_id,frame,cleanup_token,capture_instance_id)
   VALUES(NEW.id,q.id,environment_qualification_execution_frame(q,NEW)||CASE WHEN b.capture_instance_id IS NULL THEN '{}'::jsonb ELSE jsonb_build_object('capture_instance_id',b.capture_instance_id) END,gen_random_uuid(),b.capture_instance_id);
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_environment_qualification_attempt_replacement() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.reserved_instance_id IS DISTINCT FROM OLD.reserved_instance_id AND EXISTS(
  SELECT 1 FROM environment_qualification_executions WHERE request_id=OLD.id AND retired_at IS NULL) THEN
  RAISE EXCEPTION 'qualification attempt replacement requires physical retirement' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd

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
 -- Retirement must remain available after expiry, supersession and parent
 -- deletion. It cannot grant execution or qualification evidence.
 IF TG_OP='UPDATE' AND NEW.state IN ('parked','stopped','failed','evicting_account_deleting') THEN RETURN NEW; END IF;
 IF q.id IS NOT NULL THEN
  PERFORM s.id FROM environment_git_sources s JOIN environment_workload_graphs g ON g.source_id=s.id
   WHERE g.id=q.graph_id FOR UPDATE OF s;
  PERFORM a.id FROM apps a JOIN environment_workload_graphs g ON g.id=q.graph_id
   JOIN LATERAL jsonb_array_elements(g.members) m ON a.id=(m->>'app_id')::uuid ORDER BY a.id FOR UPDATE OF a;
  SELECT * INTO q FROM environment_workload_qualification_requests WHERE id=q.id AND deployment_id=NEW.deployment_id FOR UPDATE;
 END IF;
 IF q.id IS NULL OR q.phase<>'claimed' OR q.execution_mode='job' OR q.lease_until<=clock_timestamp()
  OR q.lease_token IS DISTINCT FROM current_setting('gregale.gitops_qualification',true)
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
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_environment_qualification_restore_retirement() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF OLD.retired_at IS NULL AND NEW.retired_at IS NOT NULL AND NEW.capture_instance_id IS NOT NULL
  AND NEW.retirement->>'kind'='native_retired' AND EXISTS(SELECT 1 FROM environment_qualification_snapshot_receipts r
   WHERE r.instance_id=NEW.capture_instance_id AND r.snapshot->>'native_generation'=NEW.retirement->>'native_generation') THEN
  RAISE EXCEPTION 'restore target cannot borrow capture producer retirement' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS environment_qualification_restore_retirement_guard ON environment_qualification_executions;
CREATE TRIGGER environment_qualification_restore_retirement_guard BEFORE UPDATE ON environment_qualification_executions
 FOR EACH ROW EXECUTE FUNCTION guard_environment_qualification_restore_retirement();
-- +goose StatementEnd
-- +goose Down
-- Retain reservations and physical retirement fences across binary rollback.
SELECT 1;
