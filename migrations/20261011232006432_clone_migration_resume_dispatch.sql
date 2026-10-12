-- ADR-590: retain source cancel/resume dispatch before RPC.
-- +goose Up
CREATE TABLE IF NOT EXISTS project_environment_clone_migration_resumes (
 dispatch_id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 instance_id uuid NOT NULL, app_id uuid NOT NULL, deployment_id uuid NOT NULL,
 wake_id uuid NOT NULL, request_reason text NOT NULL CHECK(request_reason LIKE 'environment-clone:%'),
 lease_token text NOT NULL CHECK(lease_token<>''), source_node_id text NOT NULL CHECK(source_node_id<>''),
 lease_created_at timestamptz NOT NULL, lease_expires_at timestamptz NOT NULL,
 dispatched_at timestamptz NOT NULL DEFAULT clock_timestamp(), UNIQUE(instance_id,lease_token)
);
CREATE INDEX IF NOT EXISTS clone_migration_resume_deployment ON project_environment_clone_migration_resumes(deployment_id);
CREATE INDEX IF NOT EXISTS clone_migration_resume_reason ON project_environment_clone_migration_resumes(request_reason);
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION lock_clone_checkpoint_scheduler_admission() RETURNS void LANGUAGE plpgsql AS $$
BEGIN
 LOCK TABLE deployments IN SHARE ROW EXCLUSIVE MODE;
 LOCK TABLE instances IN SHARE ROW EXCLUSIVE MODE;
 LOCK TABLE environment_qualification_executions IN SHARE ROW EXCLUSIVE MODE;
 LOCK TABLE migration_leases IN SHARE ROW EXCLUSIVE MODE;
 LOCK TABLE project_environment_clone_wake_dispatches IN SHARE ROW EXCLUSIVE MODE;
 LOCK TABLE project_environment_clone_migration_adoptions IN SHARE ROW EXCLUSIVE MODE;
 LOCK TABLE project_environment_clone_migration_resumes IN SHARE ROW EXCLUSIVE MODE;
END $$;
CREATE OR REPLACE FUNCTION guard_clone_migration_resume() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP<>'INSERT' THEN
  RAISE EXCEPTION 'original clone resume dispatch must be retained' USING ERRCODE='23514',CONSTRAINT='clone_checkpoint_migration_resume_retained';
 END IF;
 IF current_setting('transaction_isolation')<>'read committed' THEN
  RAISE EXCEPTION 'resume dispatch requires a current snapshot' USING ERRCODE='40001';
 END IF;
 IF clone_checkpoint_deployment_fenced(NEW.deployment_id,NEW.request_reason) THEN
  RAISE EXCEPTION 'clone resume request permanently fenced' USING ERRCODE='23514',CONSTRAINT='clone_checkpoint_scheduler_request_fenced';
 END IF;
 IF NOT EXISTS(SELECT 1 FROM instances i JOIN deployments d ON d.id=i.deployment_id JOIN migration_leases m ON m.instance_id=i.id
 WHERE (i.id,i.app_id,i.deployment_id,i.wake_id,d.reason,i.node_id::text)
 = (NEW.instance_id,NEW.app_id,NEW.deployment_id,NEW.wake_id,NEW.request_reason,NEW.source_node_id)
 AND i.state IN ('running','migrating','parked')
 AND (m.lease_token,m.source_node_id,m.created_at,m.lease_expires_at)
 = (NEW.lease_token,NEW.source_node_id,NEW.lease_created_at,NEW.lease_expires_at)) THEN
  RAISE EXCEPTION 'original resume ownership changed' USING ERRCODE='23514',CONSTRAINT='clone_checkpoint_migration_resume_owner';
 END IF;
 RETURN NEW;
END $$;
CREATE OR REPLACE TRIGGER clone_migration_resume BEFORE INSERT OR UPDATE OR DELETE ON project_environment_clone_migration_resumes
 FOR EACH ROW EXECUTE FUNCTION guard_clone_migration_resume();
CREATE OR REPLACE TRIGGER clone_migration_resume_truncate BEFORE TRUNCATE ON project_environment_clone_migration_resumes
 FOR EACH STATEMENT EXECUTE FUNCTION guard_clone_migration_resume();
CREATE OR REPLACE FUNCTION record_clone_checkpoint_migration_resume(instance uuid, source text, token text)
RETURNS void LANGUAGE plpgsql AS $$
DECLARE owner instances%ROWTYPE; reason text; original migration_leases%ROWTYPE;
BEGIN
 IF current_setting('transaction_isolation')<>'read committed' THEN
  RAISE EXCEPTION 'resume dispatch requires a current snapshot' USING ERRCODE='40001';
 END IF;
 LOCK TABLE deployments IN ROW EXCLUSIVE MODE;
 LOCK TABLE instances IN ROW EXCLUSIVE MODE;
 LOCK TABLE migration_leases IN ROW EXCLUSIVE MODE;
 SELECT * INTO owner FROM instances WHERE id=instance FOR UPDATE;
 IF NOT FOUND THEN
  RAISE EXCEPTION 'resume dispatch ownership missing' USING ERRCODE='23514',CONSTRAINT='clone_checkpoint_migration_resume_owner';
 END IF;
 SELECT d.reason INTO reason FROM deployments d WHERE d.id=owner.deployment_id;
 IF clone_checkpoint_deployment_fenced(owner.deployment_id,reason) THEN
  RAISE EXCEPTION 'clone resume request permanently fenced' USING ERRCODE='23514',CONSTRAINT='clone_checkpoint_scheduler_request_fenced';
 END IF;
 IF reason LIKE 'environment-clone:%' THEN
  SELECT * INTO original FROM migration_leases WHERE instance_id=instance FOR SHARE;
  IF NOT FOUND OR (original.lease_token,original.source_node_id) IS DISTINCT FROM (token,source) THEN
   RAISE EXCEPTION 'original resume lease changed' USING ERRCODE='23514',CONSTRAINT='clone_checkpoint_migration_resume_owner';
  END IF;
  INSERT INTO project_environment_clone_migration_resumes(instance_id,app_id,deployment_id,wake_id,request_reason,lease_token,source_node_id,lease_created_at,lease_expires_at)
  VALUES(instance,owner.app_id,owner.deployment_id,owner.wake_id,reason,token,source,original.created_at,original.lease_expires_at);
 END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
SELECT lock_clone_checkpoint_scheduler_admission();
LOCK TABLE project_environment_clone_checkpoint_workload_retirements IN ACCESS EXCLUSIVE MODE;
LOCK TABLE project_environment_clone_migration_resumes IN ACCESS EXCLUSIVE MODE;
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM project_environment_clone_migration_resumes) OR EXISTS(
 SELECT 1 FROM project_environment_clone_checkpoint_workload_retirements WHERE state IN ('requested','retired')) THEN
  RAISE EXCEPTION 'clone resume evidence must be retained' USING ERRCODE='23514',CONSTRAINT='clone_checkpoint_migration_resume_down_no_evidence';
 END IF;
END $$;
DROP FUNCTION record_clone_checkpoint_migration_resume(uuid,text,text);
DROP TABLE project_environment_clone_migration_resumes;
DROP FUNCTION guard_clone_migration_resume();
CREATE OR REPLACE FUNCTION lock_clone_checkpoint_scheduler_admission() RETURNS void LANGUAGE plpgsql AS $$
BEGIN
 LOCK TABLE deployments IN SHARE ROW EXCLUSIVE MODE;
 LOCK TABLE instances IN SHARE ROW EXCLUSIVE MODE;
 LOCK TABLE environment_qualification_executions IN SHARE ROW EXCLUSIVE MODE;
 LOCK TABLE migration_leases IN SHARE ROW EXCLUSIVE MODE;
 LOCK TABLE project_environment_clone_wake_dispatches IN SHARE ROW EXCLUSIVE MODE;
 LOCK TABLE project_environment_clone_migration_adoptions IN SHARE ROW EXCLUSIVE MODE;
END $$;
-- +goose StatementEnd
