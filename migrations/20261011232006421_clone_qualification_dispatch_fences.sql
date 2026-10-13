-- ADR-590: close dispatch of previously admitted qualification executions.
-- +goose Up
CREATE INDEX IF NOT EXISTS clone_qualification_pending_deployment_idx ON environment_qualification_executions((frame->>'deployment_id')) WHERE retired_at IS NULL;
CREATE INDEX IF NOT EXISTS clone_qualification_pending_environment_idx ON environment_qualification_executions((frame->>'environment_id')) WHERE retired_at IS NULL;
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION lock_clone_checkpoint_scheduler_admission() RETURNS void LANGUAGE plpgsql AS $$
BEGIN
 LOCK TABLE deployments IN SHARE ROW EXCLUSIVE MODE;
 LOCK TABLE instances IN SHARE ROW EXCLUSIVE MODE;
 LOCK TABLE environment_qualification_executions IN SHARE ROW EXCLUSIVE MODE;
END $$;

-- Cleanup writes the frame before stopping its instance. Reserve instance
-- writer intent first so it cannot hold a frame writer while waiting for a
-- clone transaction that already owns the instance fence.
CREATE OR REPLACE FUNCTION lock_environment_qualification_cleanup_writers() RETURNS void LANGUAGE plpgsql AS $$
BEGIN
 LOCK TABLE instances IN ROW EXCLUSIVE MODE;
END $$;

CREATE OR REPLACE FUNCTION guard_clone_checkpoint_qualification_dispatch() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE fenced boolean;
BEGIN
 IF TG_OP='TRUNCATE' THEN
  RAISE EXCEPTION 'qualification execution ownership must be retained' USING ERRCODE='23514',CONSTRAINT='clone_checkpoint_execution_retained';
 END IF;
 -- Retirement remains available with the original cleanup capability. Dispatch
 -- after admission is a separate effect and needs a current retirement snapshot.
 IF TG_OP='INSERT' OR (NOT OLD.dispatch_started AND NEW.dispatch_started) THEN
  IF current_setting('transaction_isolation')<>'read committed' THEN
   RAISE EXCEPTION 'qualification dispatch requires a current snapshot' USING ERRCODE='40001';
  END IF;
  SELECT EXISTS(SELECT 1 FROM project_environment_clone_checkpoint_workload_retirements r
   WHERE r.state IN ('requested','retired') AND (
    (coalesce(r.plan->>'EnvironmentID','')<>'' AND r.plan->>'EnvironmentID'=NEW.frame->>'environment_id') OR
    r.plan @> jsonb_build_object('Workloads',jsonb_build_array(jsonb_build_object('TargetDeploymentID',NEW.frame->>'deployment_id'))) OR
    EXISTS(SELECT 1 FROM deployments d WHERE d.id::text=NEW.frame->>'deployment_id' AND d.reason='environment-clone:'||r.operation_id::text))) INTO fenced;
  IF fenced THEN
   RAISE EXCEPTION 'clone qualification dispatch permanently fenced' USING ERRCODE='23514',CONSTRAINT='clone_checkpoint_scheduler_request_fenced';
  END IF;
 END IF;
 RETURN NEW;
END $$;
CREATE OR REPLACE TRIGGER clone_checkpoint_qualification_dispatch BEFORE INSERT OR UPDATE ON environment_qualification_executions
 FOR EACH ROW EXECUTE FUNCTION guard_clone_checkpoint_qualification_dispatch();
CREATE OR REPLACE TRIGGER clone_checkpoint_qualification_retention BEFORE TRUNCATE ON environment_qualification_executions
 FOR EACH STATEMENT EXECUTE FUNCTION guard_clone_checkpoint_qualification_dispatch();
-- +goose StatementEnd

-- +goose Down
LOCK TABLE project_environment_clone_checkpoint_workload_retirements IN ACCESS EXCLUSIVE MODE;
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM project_environment_clone_checkpoint_workload_retirements WHERE state IN ('requested','retired')) THEN
  RAISE EXCEPTION 'clone execution fences must be retained' USING ERRCODE='23514',CONSTRAINT='clone_checkpoint_execution_fence_down_no_evidence';
 END IF;
END $$;
DROP TRIGGER clone_checkpoint_qualification_dispatch ON environment_qualification_executions;
DROP TRIGGER clone_checkpoint_qualification_retention ON environment_qualification_executions;
DROP FUNCTION guard_clone_checkpoint_qualification_dispatch();
DROP FUNCTION lock_environment_qualification_cleanup_writers();
CREATE OR REPLACE FUNCTION lock_clone_checkpoint_scheduler_admission() RETURNS void LANGUAGE plpgsql AS $$
BEGIN
 LOCK TABLE deployments IN SHARE ROW EXCLUSIVE MODE;
 LOCK TABLE instances IN SHARE ROW EXCLUSIVE MODE;
END $$;
-- +goose StatementEnd
DROP INDEX clone_qualification_pending_deployment_idx;
DROP INDEX clone_qualification_pending_environment_idx;
