-- ADR-568: distinguish a proven empty native attempt from a retired VM.
-- +goose Up
ALTER TABLE environment_qualification_executions
 DROP CONSTRAINT IF EXISTS environment_qualification_no_effects_receipt_canonical;
ALTER TABLE environment_qualification_executions
 ADD CONSTRAINT environment_qualification_no_effects_receipt_canonical CHECK(
  retirement IS NULL OR retirement->>'kind'<>'native_effects_absent' OR
  (retirement->>'receipt_id'=(retirement->>'receipt_id')::uuid::text AND
   retirement->>'kernel_boot_id'=(retirement->>'kernel_boot_id')::uuid::text AND
   NOT (retirement ? 'native_generation')));
CREATE UNIQUE INDEX IF NOT EXISTS environment_qualification_no_effects_receipt_unique_idx
 ON environment_qualification_executions((retirement->>'receipt_id'))
 WHERE retirement->>'kind'='native_effects_absent';

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
  ELSIF NEW.retirement->>'kind'='native_retired' THEN
   IF NEW.retirement->'processes_exited' IS DISTINCT FROM 'true'::jsonb OR NEW.retirement->'resources_removed' IS DISTINCT FROM 'true'::jsonb
    OR coalesce((NEW.retirement->>'receipt_id')::uuid,'00000000-0000-0000-0000-000000000000')='00000000-0000-0000-0000-000000000000'
    OR coalesce((NEW.retirement->>'native_generation')::uuid,'00000000-0000-0000-0000-000000000000')='00000000-0000-0000-0000-000000000000'
    OR coalesce((NEW.retirement->>'kernel_boot_id')::uuid,'00000000-0000-0000-0000-000000000000')='00000000-0000-0000-0000-000000000000'
    OR NEW.retirement-(ARRAY['kind','receipt_id','native_generation','kernel_boot_id','processes_exited','resources_removed'])<>'{}'::jsonb THEN
    RAISE EXCEPTION 'qualification retirement requires complete native evidence' USING ERRCODE='23514';
   END IF;
  ELSIF NEW.retirement->>'kind'='native_effects_absent' THEN
   IF NEW.retirement->'processes_exited' IS DISTINCT FROM 'true'::jsonb OR NEW.retirement->'resources_removed' IS DISTINCT FROM 'true'::jsonb
    OR coalesce((NEW.retirement->>'receipt_id')::uuid,'00000000-0000-0000-0000-000000000000')='00000000-0000-0000-0000-000000000000'
    OR coalesce((NEW.retirement->>'kernel_boot_id')::uuid,'00000000-0000-0000-0000-000000000000')='00000000-0000-0000-0000-000000000000'
    OR NEW.retirement ? 'native_generation'
    OR NEW.retirement-(ARRAY['kind','receipt_id','kernel_boot_id','processes_exited','resources_removed'])<>'{}'::jsonb THEN
    RAISE EXCEPTION 'qualification no-effects evidence is incomplete' USING ERRCODE='23514';
   END IF;
  ELSE
   RAISE EXCEPTION 'qualification retirement kind is unsupported' USING ERRCODE='23514';
  END IF;
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd

-- +goose Down
DROP INDEX IF EXISTS environment_qualification_no_effects_receipt_unique_idx;
ALTER TABLE environment_qualification_executions DROP CONSTRAINT IF EXISTS environment_qualification_no_effects_receipt_canonical;

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
