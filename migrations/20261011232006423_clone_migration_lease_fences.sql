-- ADR-590: retain unresolved original migration ownership after clone fencing.
-- +goose Up
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION lock_clone_checkpoint_scheduler_admission() RETURNS void LANGUAGE plpgsql AS $$
BEGIN
 LOCK TABLE deployments IN SHARE ROW EXCLUSIVE MODE;
 LOCK TABLE instances IN SHARE ROW EXCLUSIVE MODE;
 LOCK TABLE environment_qualification_executions IN SHARE ROW EXCLUSIVE MODE;
 LOCK TABLE migration_leases IN SHARE ROW EXCLUSIVE MODE;
END $$;

CREATE OR REPLACE FUNCTION guard_clone_checkpoint_migration_lease() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE identity uuid; fenced boolean;
BEGIN
 IF TG_OP='TRUNCATE' THEN
  IF current_setting('transaction_isolation')<>'read committed' THEN RAISE EXCEPTION 'migration retention requires a current snapshot' USING ERRCODE='40001'; END IF;
  IF EXISTS(SELECT 1 FROM migration_leases m JOIN instances i ON i.id=m.instance_id LEFT JOIN deployments d ON d.id=i.deployment_id
   WHERE clone_checkpoint_deployment_fenced(i.deployment_id,d.reason)) THEN
   RAISE EXCEPTION 'original clone migration ownership must be retained' USING ERRCODE='23514',CONSTRAINT='clone_checkpoint_migration_retained';
  END IF;
  RETURN NULL;
 END IF;
 IF TG_OP='INSERT' THEN identity:=NEW.instance_id; ELSE identity:=OLD.instance_id; END IF;
 SELECT clone_checkpoint_deployment_fenced(i.deployment_id,d.reason) INTO fenced
 FROM instances i LEFT JOIN deployments d ON d.id=i.deployment_id WHERE i.id=identity;
 IF TG_OP IN ('INSERT','DELETE') OR TG_OP='UPDATE' AND NEW IS DISTINCT FROM OLD THEN
  IF current_setting('transaction_isolation')<>'read committed' THEN
   RAISE EXCEPTION 'migration admission requires a current snapshot' USING ERRCODE='40001';
  END IF;
 END IF;
 IF NOT coalesce(fenced,false) AND TG_OP='UPDATE' AND NEW.instance_id IS DISTINCT FROM OLD.instance_id THEN
  SELECT clone_checkpoint_deployment_fenced(i.deployment_id,d.reason) INTO fenced
  FROM instances i LEFT JOIN deployments d ON d.id=i.deployment_id WHERE i.id=NEW.instance_id;
 END IF;
 IF fenced THEN
  IF TG_OP='INSERT' THEN
   RAISE EXCEPTION 'clone migration request permanently fenced' USING ERRCODE='23514',CONSTRAINT='clone_checkpoint_scheduler_request_fenced';
  ELSIF TG_OP='DELETE' THEN
   RAISE EXCEPTION 'original clone migration ownership must be retained' USING ERRCODE='23514',CONSTRAINT='clone_checkpoint_migration_retained';
  ELSIF (NEW.instance_id,NEW.lease_token,NEW.source_node_id,NEW.created_at,NEW.lease_expires_at)
   IS DISTINCT FROM (OLD.instance_id,OLD.lease_token,OLD.source_node_id,OLD.created_at,OLD.lease_expires_at)
   OR (NOT OLD.pending AND NEW.pending)
   OR (NOT OLD.pending AND (NEW.mem_storage_key,NEW.vmstate_storage_key) IS DISTINCT FROM (OLD.mem_storage_key,OLD.vmstate_storage_key)) THEN
   RAISE EXCEPTION 'clone migration authority cannot be renewed or replaced' USING ERRCODE='23514',CONSTRAINT='clone_checkpoint_scheduler_request_fenced';
  END IF;
 END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF;
 RETURN NEW;
END $$;
CREATE OR REPLACE TRIGGER clone_checkpoint_migration_lease BEFORE INSERT OR UPDATE OR DELETE ON migration_leases
 FOR EACH ROW EXECUTE FUNCTION guard_clone_checkpoint_migration_lease();
CREATE OR REPLACE TRIGGER clone_checkpoint_migration_retention BEFORE TRUNCATE ON migration_leases
 FOR EACH STATEMENT EXECUTE FUNCTION guard_clone_checkpoint_migration_lease();

CREATE OR REPLACE FUNCTION guard_clone_checkpoint_migration_instance() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF current_setting('transaction_isolation')<>'read committed' THEN RAISE EXCEPTION 'instance deletion requires a current snapshot' USING ERRCODE='40001'; END IF;
 IF EXISTS(SELECT 1 FROM migration_leases WHERE instance_id=OLD.id) AND clone_checkpoint_deployment_fenced(OLD.deployment_id,(SELECT reason FROM deployments WHERE id=OLD.deployment_id)) THEN
  RAISE EXCEPTION 'clone instance retains original migration ownership' USING ERRCODE='23514',CONSTRAINT='clone_checkpoint_migration_retained';
 END IF;
 RETURN OLD;
END $$;
CREATE OR REPLACE TRIGGER clone_checkpoint_migration_instance BEFORE DELETE ON instances
 FOR EACH ROW EXECUTE FUNCTION guard_clone_checkpoint_migration_instance();
CREATE OR REPLACE FUNCTION guard_clone_checkpoint_migration_deployment() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF current_setting('transaction_isolation')<>'read committed' THEN RAISE EXCEPTION 'deployment deletion requires a current snapshot' USING ERRCODE='40001'; END IF;
 IF clone_checkpoint_deployment_fenced(OLD.id,OLD.reason) AND EXISTS(
 SELECT 1 FROM instances i JOIN migration_leases m ON m.instance_id=i.id WHERE i.deployment_id=OLD.id) THEN
  RAISE EXCEPTION 'clone deployment retains original migration ownership' USING ERRCODE='23514',CONSTRAINT='clone_checkpoint_migration_retained';
 END IF;
 RETURN OLD;
END $$;
CREATE OR REPLACE TRIGGER clone_checkpoint_migration_deployment BEFORE DELETE ON deployments
 FOR EACH ROW EXECUTE FUNCTION guard_clone_checkpoint_migration_deployment();
-- +goose StatementEnd

-- +goose Down
LOCK TABLE project_environment_clone_checkpoint_workload_retirements IN ACCESS EXCLUSIVE MODE;
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM project_environment_clone_checkpoint_workload_retirements WHERE state IN ('requested','retired')) THEN
  RAISE EXCEPTION 'clone migration fences must be retained' USING ERRCODE='23514',CONSTRAINT='clone_checkpoint_migration_down_no_evidence';
 END IF;
END $$;
DROP TRIGGER clone_checkpoint_migration_deployment ON deployments;
DROP TRIGGER clone_checkpoint_migration_instance ON instances;
DROP TRIGGER clone_checkpoint_migration_lease ON migration_leases;
DROP TRIGGER clone_checkpoint_migration_retention ON migration_leases;
DROP FUNCTION guard_clone_checkpoint_migration_deployment();
DROP FUNCTION guard_clone_checkpoint_migration_instance();
DROP FUNCTION guard_clone_checkpoint_migration_lease();
CREATE OR REPLACE FUNCTION lock_clone_checkpoint_scheduler_admission() RETURNS void LANGUAGE plpgsql AS $$
BEGIN
 LOCK TABLE deployments IN SHARE ROW EXCLUSIVE MODE;
 LOCK TABLE instances IN SHARE ROW EXCLUSIVE MODE;
 LOCK TABLE environment_qualification_executions IN SHARE ROW EXCLUSIVE MODE;
END $$;
-- +goose StatementEnd
