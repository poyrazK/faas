-- ADR-590: retain original destination adoption dispatch before RPC.
-- +goose Up
CREATE TABLE IF NOT EXISTS project_environment_clone_migration_adoptions (
 dispatch_id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 instance_id uuid NOT NULL,
 app_id uuid NOT NULL,
 deployment_id uuid NOT NULL,
 wake_id uuid NOT NULL,
 request_reason text NOT NULL CHECK(request_reason LIKE 'environment-clone:%'),
 lease_token text NOT NULL CHECK(lease_token<>''),
 source_node_id text NOT NULL CHECK(source_node_id<>''),
 destination_node_id uuid NOT NULL,
 lease_created_at timestamptz NOT NULL,
 lease_expires_at timestamptz NOT NULL,
 mem_storage_key text NOT NULL CHECK(mem_storage_key<>''),
 vmstate_storage_key text NOT NULL CHECK(vmstate_storage_key<>''),
 dispatched_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(instance_id,lease_token)
);
CREATE INDEX IF NOT EXISTS clone_migration_adoption_deployment ON project_environment_clone_migration_adoptions(deployment_id);
CREATE INDEX IF NOT EXISTS clone_migration_adoption_reason ON project_environment_clone_migration_adoptions(request_reason);
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION lock_clone_checkpoint_scheduler_admission() RETURNS void LANGUAGE plpgsql AS $$
BEGIN
 LOCK TABLE deployments IN SHARE ROW EXCLUSIVE MODE;
 LOCK TABLE instances IN SHARE ROW EXCLUSIVE MODE;
 LOCK TABLE environment_qualification_executions IN SHARE ROW EXCLUSIVE MODE;
 LOCK TABLE migration_leases IN SHARE ROW EXCLUSIVE MODE;
 LOCK TABLE project_environment_clone_wake_dispatches IN SHARE ROW EXCLUSIVE MODE;
 LOCK TABLE project_environment_clone_migration_adoptions IN SHARE ROW EXCLUSIVE MODE;
END $$;
CREATE OR REPLACE FUNCTION guard_clone_migration_adoption() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP<>'INSERT' THEN
  RAISE EXCEPTION 'original clone adoption dispatch must be retained' USING ERRCODE='23514',CONSTRAINT='clone_checkpoint_migration_adoption_retained';
 END IF;
 IF current_setting('transaction_isolation')<>'read committed' THEN
  RAISE EXCEPTION 'adoption dispatch requires a current snapshot' USING ERRCODE='40001';
 END IF;
 IF clone_checkpoint_deployment_fenced(NEW.deployment_id,NEW.request_reason) THEN
  RAISE EXCEPTION 'clone adoption request permanently fenced' USING ERRCODE='23514',CONSTRAINT='clone_checkpoint_scheduler_request_fenced';
 END IF;
 IF NOT EXISTS(SELECT 1 FROM instances i JOIN deployments d ON d.id=i.deployment_id JOIN migration_leases m ON m.instance_id=i.id
 WHERE (i.id,i.app_id,i.deployment_id,i.wake_id,d.reason,i.lease_token,i.node_id::text,i.state)
 = (NEW.instance_id,NEW.app_id,NEW.deployment_id,NEW.wake_id,NEW.request_reason,NEW.lease_token,NEW.source_node_id,'migrating')
 AND NOT m.pending AND m.lease_expires_at>clock_timestamp()
 AND (m.lease_token,m.source_node_id,m.created_at,m.lease_expires_at,m.mem_storage_key,m.vmstate_storage_key)
 = (NEW.lease_token,NEW.source_node_id,NEW.lease_created_at,NEW.lease_expires_at,NEW.mem_storage_key,NEW.vmstate_storage_key)
 AND NEW.destination_node_id::text<>NEW.source_node_id
 AND EXISTS(SELECT 1 FROM compute_nodes n WHERE n.id=NEW.destination_node_id)) THEN
  RAISE EXCEPTION 'original adoption ownership changed' USING ERRCODE='23514',CONSTRAINT='clone_checkpoint_migration_adoption_owner';
 END IF;
 RETURN NEW;
END $$;
CREATE OR REPLACE TRIGGER clone_migration_adoption BEFORE INSERT OR UPDATE OR DELETE ON project_environment_clone_migration_adoptions
 FOR EACH ROW EXECUTE FUNCTION guard_clone_migration_adoption();
CREATE OR REPLACE TRIGGER clone_migration_adoption_truncate BEFORE TRUNCATE ON project_environment_clone_migration_adoptions
 FOR EACH STATEMENT EXECUTE FUNCTION guard_clone_migration_adoption();
CREATE OR REPLACE FUNCTION record_clone_checkpoint_migration_adoption(instance uuid, app uuid, deployment uuid, destination uuid, mem_key text, state_key text, token text)
RETURNS void LANGUAGE plpgsql AS $$
DECLARE owner instances%ROWTYPE; reason text; original migration_leases%ROWTYPE;
BEGIN
 IF current_setting('transaction_isolation')<>'read committed' THEN
  RAISE EXCEPTION 'adoption dispatch requires a current snapshot' USING ERRCODE='40001';
 END IF;
 LOCK TABLE deployments IN ROW EXCLUSIVE MODE;
 LOCK TABLE instances IN ROW EXCLUSIVE MODE;
 LOCK TABLE migration_leases IN ROW EXCLUSIVE MODE;
 SELECT * INTO owner FROM instances WHERE id=instance FOR UPDATE;
 IF NOT FOUND OR (owner.app_id,owner.deployment_id) IS DISTINCT FROM (app,deployment) OR owner.state<>'migrating' THEN
  RAISE EXCEPTION 'adoption dispatch ownership changed' USING ERRCODE='23514',CONSTRAINT='clone_checkpoint_migration_adoption_owner';
 END IF;
 SELECT d.reason INTO reason FROM deployments d WHERE d.id=deployment;
 IF clone_checkpoint_deployment_fenced(deployment,reason) THEN
  RAISE EXCEPTION 'clone adoption request permanently fenced' USING ERRCODE='23514',CONSTRAINT='clone_checkpoint_scheduler_request_fenced';
 END IF;
 IF reason LIKE 'environment-clone:%' THEN
  SELECT * INTO original FROM migration_leases WHERE instance_id=instance FOR SHARE;
  IF NOT FOUND OR original.pending OR original.lease_expires_at<=clock_timestamp()
   OR (original.lease_token,original.mem_storage_key,original.vmstate_storage_key) IS DISTINCT FROM (token,mem_key,state_key) THEN
   RAISE EXCEPTION 'original adoption lease changed' USING ERRCODE='23514',CONSTRAINT='clone_checkpoint_migration_adoption_owner';
  END IF;
  INSERT INTO project_environment_clone_migration_adoptions(instance_id,app_id,deployment_id,wake_id,request_reason,lease_token,source_node_id,destination_node_id,lease_created_at,lease_expires_at,mem_storage_key,vmstate_storage_key)
  VALUES(instance,app,deployment,owner.wake_id,reason,token,original.source_node_id,destination,original.created_at,original.lease_expires_at,mem_key,state_key);
 END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
SELECT lock_clone_checkpoint_scheduler_admission();
LOCK TABLE project_environment_clone_checkpoint_workload_retirements IN ACCESS EXCLUSIVE MODE;
LOCK TABLE project_environment_clone_migration_adoptions IN ACCESS EXCLUSIVE MODE;
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM project_environment_clone_migration_adoptions) OR EXISTS(
 SELECT 1 FROM project_environment_clone_checkpoint_workload_retirements WHERE state IN ('requested','retired')) THEN
  RAISE EXCEPTION 'clone adoption evidence must be retained' USING ERRCODE='23514',CONSTRAINT='clone_checkpoint_migration_adoption_down_no_evidence';
 END IF;
END $$;
DROP FUNCTION record_clone_checkpoint_migration_adoption(uuid,uuid,uuid,uuid,text,text,text);
DROP TABLE project_environment_clone_migration_adoptions;
DROP FUNCTION guard_clone_migration_adoption();
CREATE OR REPLACE FUNCTION lock_clone_checkpoint_scheduler_admission() RETURNS void LANGUAGE plpgsql AS $$
BEGIN
 LOCK TABLE deployments IN SHARE ROW EXCLUSIVE MODE;
 LOCK TABLE instances IN SHARE ROW EXCLUSIVE MODE;
 LOCK TABLE environment_qualification_executions IN SHARE ROW EXCLUSIVE MODE;
 LOCK TABLE migration_leases IN SHARE ROW EXCLUSIVE MODE;
 LOCK TABLE project_environment_clone_wake_dispatches IN SHARE ROW EXCLUSIVE MODE;
END $$;
-- +goose StatementEnd
