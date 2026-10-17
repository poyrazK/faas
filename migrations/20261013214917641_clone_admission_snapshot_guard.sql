-- ADR-590 / ADR-570: snapshot-safe clone retirement fences for repeatable-read
-- admission writers. Read-committed writers keep the original fence path.
-- ADR-570 traffic mutations write and delete deployments inside a repeatable-read
-- snapshot taken under their account locks. Every retirement request bumps one
-- guard row after its table guards; such a writer locks that row FOR SHARE,
-- which raises a serialization failure when a newer retirement committed after
-- its snapshot and waits for one still in flight. No session setting bypasses it.
-- +goose Up
CREATE TABLE IF NOT EXISTS clone_checkpoint_admission_guard (
 id smallint PRIMARY KEY CHECK (id = 1),
 generation bigint NOT NULL DEFAULT 0 CHECK (generation >= 0)
);
INSERT INTO clone_checkpoint_admission_guard(id) VALUES (1) ON CONFLICT (id) DO NOTHING;
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION clone_checkpoint_admission_snapshot_guard() RETURNS void LANGUAGE plpgsql AS $$
BEGIN
 PERFORM 1 FROM clone_checkpoint_admission_guard WHERE id=1 FOR SHARE;
 IF NOT FOUND THEN
  RAISE EXCEPTION 'clone admission guard is missing' USING ERRCODE='55000',CONSTRAINT='clone_checkpoint_admission_guard_missing';
 END IF;
END $$;
CREATE OR REPLACE FUNCTION lock_clone_checkpoint_scheduler_admission() RETURNS void LANGUAGE plpgsql AS $$
BEGIN
 LOCK TABLE deployments IN SHARE ROW EXCLUSIVE MODE;
 LOCK TABLE instances IN SHARE ROW EXCLUSIVE MODE;
 LOCK TABLE environment_qualification_executions IN SHARE ROW EXCLUSIVE MODE;
 LOCK TABLE migration_leases IN SHARE ROW EXCLUSIVE MODE;
 LOCK TABLE project_environment_clone_wake_dispatches IN SHARE ROW EXCLUSIVE MODE;
 LOCK TABLE project_environment_clone_migration_adoptions IN SHARE ROW EXCLUSIVE MODE;
 LOCK TABLE project_environment_clone_migration_resumes IN SHARE ROW EXCLUSIVE MODE;
 -- Last, after every table guard: admission writers in a non-read-committed
 -- snapshot detect this update instead of missing the new fence.
 UPDATE clone_checkpoint_admission_guard SET generation=generation+1 WHERE id=1;
END $$;
CREATE OR REPLACE FUNCTION guard_clone_checkpoint_deployment_admission() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF (TG_OP='INSERT' OR (NEW.id,NEW.app_id,NEW.scope,NEW.reason) IS DISTINCT FROM (OLD.id,OLD.app_id,OLD.scope,OLD.reason))
  AND current_setting('transaction_isolation')<>'read committed' THEN
  -- A snapshot older than the newest retirement request fails here (40001),
  -- so the fence check below never reads a stale retirement catalogue.
  PERFORM clone_checkpoint_admission_snapshot_guard();
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
CREATE OR REPLACE FUNCTION guard_clone_checkpoint_instance_admission() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE fenced boolean;
BEGIN
 IF (TG_OP='INSERT' OR (NEW.id,NEW.app_id,NEW.deployment_id,NEW.node_id,NEW.wake_id,NEW.mode,NEW.state) IS DISTINCT FROM (OLD.id,OLD.app_id,OLD.deployment_id,OLD.node_id,OLD.wake_id,OLD.mode,OLD.state))
  AND current_setting('transaction_isolation')<>'read committed' THEN
  -- A snapshot older than the newest retirement request fails here (40001),
  -- so the fence check below never reads a stale retirement catalogue.
  PERFORM clone_checkpoint_admission_snapshot_guard();
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
CREATE OR REPLACE FUNCTION guard_clone_checkpoint_migration_instance() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF current_setting('transaction_isolation')<>'read committed' THEN
  PERFORM clone_checkpoint_admission_snapshot_guard();
 END IF;
 IF EXISTS(SELECT 1 FROM migration_leases WHERE instance_id=OLD.id) AND clone_checkpoint_deployment_fenced(OLD.deployment_id,(SELECT reason FROM deployments WHERE id=OLD.deployment_id)) THEN
  RAISE EXCEPTION 'clone instance retains original migration ownership' USING ERRCODE='23514',CONSTRAINT='clone_checkpoint_migration_retained';
 END IF;
 RETURN OLD;
END $$;
CREATE OR REPLACE FUNCTION guard_clone_checkpoint_migration_deployment() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF current_setting('transaction_isolation')<>'read committed' THEN
  PERFORM clone_checkpoint_admission_snapshot_guard();
 END IF;
 IF clone_checkpoint_deployment_fenced(OLD.id,OLD.reason) AND EXISTS(
 SELECT 1 FROM instances i JOIN migration_leases m ON m.instance_id=i.id WHERE i.deployment_id=OLD.id) THEN
  RAISE EXCEPTION 'clone deployment retains original migration ownership' USING ERRCODE='23514',CONSTRAINT='clone_checkpoint_migration_retained';
 END IF;
 RETURN OLD;
END $$;
-- +goose StatementEnd

-- +goose Down
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
CREATE OR REPLACE FUNCTION guard_clone_checkpoint_migration_instance() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF current_setting('transaction_isolation')<>'read committed' THEN RAISE EXCEPTION 'instance deletion requires a current snapshot' USING ERRCODE='40001'; END IF;
 IF EXISTS(SELECT 1 FROM migration_leases WHERE instance_id=OLD.id) AND clone_checkpoint_deployment_fenced(OLD.deployment_id,(SELECT reason FROM deployments WHERE id=OLD.deployment_id)) THEN
  RAISE EXCEPTION 'clone instance retains original migration ownership' USING ERRCODE='23514',CONSTRAINT='clone_checkpoint_migration_retained';
 END IF;
 RETURN OLD;
END $$;
CREATE OR REPLACE FUNCTION guard_clone_checkpoint_migration_deployment() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF current_setting('transaction_isolation')<>'read committed' THEN RAISE EXCEPTION 'deployment deletion requires a current snapshot' USING ERRCODE='40001'; END IF;
 IF clone_checkpoint_deployment_fenced(OLD.id,OLD.reason) AND EXISTS(
 SELECT 1 FROM instances i JOIN migration_leases m ON m.instance_id=i.id WHERE i.deployment_id=OLD.id) THEN
  RAISE EXCEPTION 'clone deployment retains original migration ownership' USING ERRCODE='23514',CONSTRAINT='clone_checkpoint_migration_retained';
 END IF;
 RETURN OLD;
END $$;
-- +goose StatementEnd
DROP FUNCTION IF EXISTS clone_checkpoint_admission_snapshot_guard();
DROP TABLE IF EXISTS clone_checkpoint_admission_guard;
