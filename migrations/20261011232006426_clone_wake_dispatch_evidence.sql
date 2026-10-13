-- ADR-590: retain ordinary clone wake RPC ownership before dispatch.
-- +goose Up
CREATE TABLE IF NOT EXISTS project_environment_clone_wake_dispatches (
 instance_id uuid PRIMARY KEY,
 app_id uuid NOT NULL,
 deployment_id uuid NOT NULL,
 node_id uuid NOT NULL,
 wake_id uuid NOT NULL,
 request_reason text NOT NULL CHECK(request_reason LIKE 'environment-clone:%'),
 method text NOT NULL CHECK(method IN ('cold_boot','restore')),
 dispatched_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX IF NOT EXISTS clone_wake_dispatch_deployment ON project_environment_clone_wake_dispatches(deployment_id);
CREATE INDEX IF NOT EXISTS clone_wake_dispatch_reason ON project_environment_clone_wake_dispatches(request_reason);
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION lock_clone_checkpoint_scheduler_admission() RETURNS void LANGUAGE plpgsql AS $$
BEGIN
 LOCK TABLE deployments IN SHARE ROW EXCLUSIVE MODE;
 LOCK TABLE instances IN SHARE ROW EXCLUSIVE MODE;
 LOCK TABLE environment_qualification_executions IN SHARE ROW EXCLUSIVE MODE;
 LOCK TABLE migration_leases IN SHARE ROW EXCLUSIVE MODE;
 LOCK TABLE project_environment_clone_wake_dispatches IN SHARE ROW EXCLUSIVE MODE;
END $$;
CREATE OR REPLACE FUNCTION guard_clone_wake_dispatch() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP<>'INSERT' THEN
  RAISE EXCEPTION 'original clone wake dispatch must be retained' USING ERRCODE='23514',CONSTRAINT='clone_checkpoint_wake_dispatch_retained';
 END IF;
 IF current_setting('transaction_isolation')<>'read committed' THEN
  RAISE EXCEPTION 'wake dispatch requires a current snapshot' USING ERRCODE='40001';
 END IF;
 IF clone_checkpoint_deployment_fenced(NEW.deployment_id,NEW.request_reason) THEN
  RAISE EXCEPTION 'clone wake request permanently fenced' USING ERRCODE='23514',CONSTRAINT='clone_checkpoint_scheduler_request_fenced';
 END IF;
 IF NOT EXISTS(SELECT 1 FROM instances i JOIN deployments d ON d.id=i.deployment_id
 WHERE i.id=NEW.instance_id AND i.app_id=NEW.app_id AND i.deployment_id=NEW.deployment_id
 AND i.node_id=NEW.node_id AND i.wake_id=NEW.wake_id AND d.reason=NEW.request_reason
 AND i.state IN ('waking','cold_booting')) THEN
  RAISE EXCEPTION 'wake dispatch ownership changed' USING ERRCODE='23514',CONSTRAINT='clone_checkpoint_wake_dispatch_owner';
 END IF;
 RETURN NEW;
END $$;
CREATE OR REPLACE TRIGGER clone_wake_dispatch BEFORE INSERT OR UPDATE OR DELETE ON project_environment_clone_wake_dispatches
 FOR EACH ROW EXECUTE FUNCTION guard_clone_wake_dispatch();
CREATE OR REPLACE TRIGGER clone_wake_dispatch_truncate BEFORE TRUNCATE ON project_environment_clone_wake_dispatches
 FOR EACH STATEMENT EXECUTE FUNCTION guard_clone_wake_dispatch();
CREATE OR REPLACE FUNCTION record_clone_checkpoint_wake_dispatch(instance uuid, app uuid, deployment uuid, node uuid, wake_method text)
RETURNS void LANGUAGE plpgsql AS $$
DECLARE owner instances%ROWTYPE; reason text;
BEGIN
 IF current_setting('transaction_isolation')<>'read committed' THEN
  RAISE EXCEPTION 'wake dispatch requires a current snapshot' USING ERRCODE='40001';
 END IF;
 -- Match the retirement fence order before reading original ownership.
 LOCK TABLE deployments IN ROW EXCLUSIVE MODE;
 LOCK TABLE instances IN ROW EXCLUSIVE MODE;
 SELECT * INTO owner FROM instances WHERE id=instance FOR UPDATE;
 IF NOT FOUND OR (owner.app_id,owner.deployment_id,owner.node_id) IS DISTINCT FROM (app,deployment,node)
 OR owner.state NOT IN ('waking','cold_booting') THEN
  RAISE EXCEPTION 'wake dispatch ownership changed' USING ERRCODE='23514',CONSTRAINT='clone_checkpoint_wake_dispatch_owner';
 END IF;
 SELECT d.reason INTO reason FROM deployments d WHERE d.id=deployment;
 IF clone_checkpoint_deployment_fenced(deployment,reason) THEN
  RAISE EXCEPTION 'clone wake request permanently fenced' USING ERRCODE='23514',CONSTRAINT='clone_checkpoint_scheduler_request_fenced';
 END IF;
 IF reason LIKE 'environment-clone:%' THEN
  INSERT INTO project_environment_clone_wake_dispatches(instance_id,app_id,deployment_id,node_id,wake_id,request_reason,method)
  VALUES(instance,app,deployment,node,owner.wake_id,reason,wake_method);
 END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
SELECT lock_clone_checkpoint_scheduler_admission();
LOCK TABLE project_environment_clone_checkpoint_workload_retirements IN ACCESS EXCLUSIVE MODE;
LOCK TABLE project_environment_clone_wake_dispatches IN ACCESS EXCLUSIVE MODE;
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM project_environment_clone_wake_dispatches) OR EXISTS(
 SELECT 1 FROM project_environment_clone_checkpoint_workload_retirements WHERE state IN ('requested','retired')) THEN
  RAISE EXCEPTION 'clone wake dispatch evidence must be retained' USING ERRCODE='23514',CONSTRAINT='clone_checkpoint_wake_dispatch_down_no_evidence';
 END IF;
END $$;
DROP FUNCTION record_clone_checkpoint_wake_dispatch(uuid,uuid,uuid,uuid,text);
DROP TABLE project_environment_clone_wake_dispatches;
DROP FUNCTION guard_clone_wake_dispatch();
CREATE OR REPLACE FUNCTION lock_clone_checkpoint_scheduler_admission() RETURNS void LANGUAGE plpgsql AS $$
BEGIN
 LOCK TABLE deployments IN SHARE ROW EXCLUSIVE MODE;
 LOCK TABLE instances IN SHARE ROW EXCLUSIVE MODE;
 LOCK TABLE environment_qualification_executions IN SHARE ROW EXCLUSIVE MODE;
 LOCK TABLE migration_leases IN SHARE ROW EXCLUSIVE MODE;
END $$;
-- +goose StatementEnd
