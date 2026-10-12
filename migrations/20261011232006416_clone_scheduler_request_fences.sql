-- ADR-590: requested native retirement permanently closes catalogue admission.
-- +goose Up
CREATE INDEX IF NOT EXISTS clone_checkpoint_scheduler_reason_fences
 ON project_environment_clone_checkpoint_workload_retirements (('environment-clone:'||operation_id::text))
 WHERE state IN ('requested','retired');
CREATE INDEX IF NOT EXISTS clone_checkpoint_scheduler_target_fences
 ON project_environment_clone_checkpoint_workload_retirements USING gin(plan jsonb_path_ops)
 WHERE state IN ('requested','retired');
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION lock_clone_checkpoint_scheduler_admission() RETURNS void LANGUAGE plpgsql AS $$
BEGIN
 LOCK TABLE deployments IN SHARE ROW EXCLUSIVE MODE;
 LOCK TABLE instances IN SHARE ROW EXCLUSIVE MODE;
END $$;

CREATE OR REPLACE FUNCTION clone_checkpoint_deployment_fenced(deployment uuid, request_reason text)
RETURNS boolean LANGUAGE sql VOLATILE AS $$
 SELECT EXISTS(SELECT 1 FROM project_environment_clone_checkpoint_workload_retirements r
 WHERE r.state IN ('requested','retired') AND
 (request_reason='environment-clone:'||r.operation_id::text OR
 r.plan @> jsonb_build_object('Workloads',jsonb_build_array(jsonb_build_object('TargetDeploymentID',deployment::text)))))
$$;

CREATE OR REPLACE FUNCTION guard_clone_checkpoint_deployment_admission() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF (TG_OP='INSERT' OR (NEW.id,NEW.app_id,NEW.scope,NEW.reason) IS DISTINCT FROM (OLD.id,OLD.app_id,OLD.scope,OLD.reason))
  AND current_setting('transaction_isolation')<>'read committed' THEN
  RAISE EXCEPTION 'deployment admission requires a current snapshot' USING ERRCODE='40001';
 END IF;
 IF clone_checkpoint_deployment_fenced(NEW.id,NEW.reason) THEN
  IF TG_OP='INSERT' THEN
   RAISE EXCEPTION 'clone deployment request permanently fenced' USING ERRCODE='23514',CONSTRAINT='clone_checkpoint_scheduler_request_fenced';
  ELSIF (NEW.id,NEW.app_id,NEW.scope,NEW.reason) IS DISTINCT FROM (OLD.id,OLD.app_id,OLD.scope,OLD.reason) THEN
   RAISE EXCEPTION 'clone deployment ownership permanently fenced' USING ERRCODE='23514',CONSTRAINT='clone_checkpoint_scheduler_request_fenced';
  END IF;
 ELSIF TG_OP='UPDATE' AND clone_checkpoint_deployment_fenced(OLD.id,OLD.reason) THEN
  RAISE EXCEPTION 'clone deployment ownership permanently fenced' USING ERRCODE='23514',CONSTRAINT='clone_checkpoint_scheduler_request_fenced';
 END IF;
 RETURN NEW;
END $$;
CREATE OR REPLACE TRIGGER clone_checkpoint_deployment_admission BEFORE INSERT OR UPDATE ON deployments
 FOR EACH ROW EXECUTE FUNCTION guard_clone_checkpoint_deployment_admission();

CREATE OR REPLACE FUNCTION guard_clone_checkpoint_instance_admission() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE fenced boolean;
BEGIN
 IF (TG_OP='INSERT' OR (NEW.id,NEW.app_id,NEW.deployment_id,NEW.node_id,NEW.wake_id,NEW.mode,NEW.state) IS DISTINCT FROM (OLD.id,OLD.app_id,OLD.deployment_id,OLD.node_id,OLD.wake_id,OLD.mode,OLD.state))
  AND current_setting('transaction_isolation')<>'read committed' THEN
  RAISE EXCEPTION 'instance admission requires a current snapshot' USING ERRCODE='40001';
 END IF;
 SELECT clone_checkpoint_deployment_fenced(NEW.deployment_id,d.reason) INTO fenced
 FROM (SELECT 1) x LEFT JOIN deployments d ON d.id=NEW.deployment_id;
 IF TG_OP='UPDATE' AND NOT fenced THEN
  SELECT clone_checkpoint_deployment_fenced(OLD.deployment_id,d.reason) INTO fenced
  FROM (SELECT 1) x LEFT JOIN deployments d ON d.id=OLD.deployment_id;
 END IF;
 IF fenced THEN
  IF TG_OP='INSERT' THEN
   RAISE EXCEPTION 'clone instance request permanently fenced' USING ERRCODE='23514',CONSTRAINT='clone_checkpoint_scheduler_request_fenced';
  ELSIF (NEW.id,NEW.app_id,NEW.deployment_id,NEW.node_id,NEW.wake_id,NEW.mode) IS DISTINCT FROM (OLD.id,OLD.app_id,OLD.deployment_id,OLD.node_id,OLD.wake_id,OLD.mode)
    OR (NEW.state IS DISTINCT FROM OLD.state AND NEW.state NOT IN ('draining','parked','stopped','failed','evicting_account_deleting')) THEN
   RAISE EXCEPTION 'clone instance restart permanently fenced' USING ERRCODE='23514',CONSTRAINT='clone_checkpoint_scheduler_request_fenced';
  END IF;
 END IF;
 RETURN NEW;
END $$;
CREATE OR REPLACE TRIGGER clone_checkpoint_instance_admission BEFORE INSERT OR UPDATE ON instances
 FOR EACH ROW EXECUTE FUNCTION guard_clone_checkpoint_instance_admission();

CREATE OR REPLACE FUNCTION guard_clone_checkpoint_scheduler_fence() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='TRUNCATE' THEN
  RAISE EXCEPTION 'clone scheduler fences cannot be truncated' USING ERRCODE='23514',CONSTRAINT='clone_checkpoint_scheduler_fence_retained';
 END IF;
 IF TG_OP='DELETE' THEN
  IF OLD.state IN ('requested','retired') THEN
   RAISE EXCEPTION 'clone scheduler fence must be retained' USING ERRCODE='23514',CONSTRAINT='clone_checkpoint_scheduler_fence_retained';
  END IF;
  RETURN OLD;
 END IF;
 IF TG_OP='UPDATE' AND OLD.state IN ('requested','retired') AND
  ((NEW.operation_id,NEW.plan,NEW.requested_at) IS DISTINCT FROM (OLD.operation_id,OLD.plan,OLD.requested_at)
   OR NEW.state='planned' OR (OLD.state='retired' AND NEW.state<>'retired')) THEN
  RAISE EXCEPTION 'clone scheduler fence is immutable' USING ERRCODE='23514',CONSTRAINT='clone_checkpoint_scheduler_fence_retained';
 END IF;
 IF NEW.state IN ('requested','retired') THEN
  PERFORM lock_clone_checkpoint_scheduler_admission();
 END IF;
 RETURN NEW;
END $$;
CREATE OR REPLACE TRIGGER clone_checkpoint_scheduler_fence BEFORE INSERT OR UPDATE OR DELETE
 ON project_environment_clone_checkpoint_workload_retirements
 FOR EACH ROW EXECUTE FUNCTION guard_clone_checkpoint_scheduler_fence();
CREATE OR REPLACE TRIGGER clone_checkpoint_scheduler_fence_truncate BEFORE TRUNCATE
 ON project_environment_clone_checkpoint_workload_retirements
 FOR EACH STATEMENT EXECUTE FUNCTION guard_clone_checkpoint_scheduler_fence();
-- +goose StatementEnd

-- +goose Down
LOCK TABLE project_environment_clone_checkpoint_workload_retirements IN ACCESS EXCLUSIVE MODE;
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM project_environment_clone_checkpoint_workload_retirements WHERE state IN ('requested','retired')) THEN
  RAISE EXCEPTION 'clone scheduler request fences must be retained' USING ERRCODE='23514',CONSTRAINT='clone_checkpoint_scheduler_fence_down_no_evidence';
 END IF;
END $$;
DROP TRIGGER clone_checkpoint_scheduler_fence_truncate ON project_environment_clone_checkpoint_workload_retirements;
DROP TRIGGER clone_checkpoint_scheduler_fence ON project_environment_clone_checkpoint_workload_retirements;
DROP TRIGGER clone_checkpoint_instance_admission ON instances;
DROP TRIGGER clone_checkpoint_deployment_admission ON deployments;
DROP INDEX clone_checkpoint_scheduler_reason_fences;
DROP INDEX clone_checkpoint_scheduler_target_fences;
DROP FUNCTION guard_clone_checkpoint_scheduler_fence();
DROP FUNCTION guard_clone_checkpoint_instance_admission();
DROP FUNCTION guard_clone_checkpoint_deployment_admission();
DROP FUNCTION clone_checkpoint_deployment_fenced(uuid,text);
DROP FUNCTION lock_clone_checkpoint_scheduler_admission();
-- +goose StatementEnd
