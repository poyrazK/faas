-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE project_environment_promotion_migrations
 ADD COLUMN IF NOT EXISTS runtime_node_id uuid REFERENCES compute_nodes(id),
 ADD COLUMN IF NOT EXISTS runtime_instance_id uuid,
 ADD COLUMN IF NOT EXISTS runtime_lease_token uuid,
 ADD COLUMN IF NOT EXISTS destroyed_at timestamptz CHECK (destroyed_at IS NULL OR isfinite(destroyed_at));
-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='project_environment_promotion_migrations'::regclass AND conname='promotion_migration_runtime_complete') THEN
  ALTER TABLE project_environment_promotion_migrations ADD CONSTRAINT promotion_migration_runtime_complete CHECK ((runtime_node_id IS NULL)=(runtime_instance_id IS NULL) AND (runtime_instance_id IS NULL)=(runtime_lease_token IS NULL));
 END IF;
END $$;
-- +goose StatementEnd
CREATE TABLE IF NOT EXISTS project_environment_promotion_migration_recoveries (
 promotion_id uuid NOT NULL REFERENCES project_environment_promotions(id) ON DELETE CASCADE,
 request_id text NOT NULL CHECK (request_id ~ '^[a-f0-9]{32}$'),
 request_hash text NOT NULL CHECK (request_hash ~ '^[a-f0-9]{64}$'),
 status text NOT NULL CHECK (status IN ('quiescing','verifying','resumed','resolved','blocked')),
 payload jsonb NOT NULL CHECK (jsonb_typeof(payload)='object' AND payload#>>'{receipt,status}'=status AND octet_length(payload::text)<=32768),
 created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(promotion_id,request_id)
);
CREATE UNIQUE INDEX IF NOT EXISTS promotion_migration_one_recovery ON project_environment_promotion_migration_recoveries(promotion_id) WHERE status<>'blocked';
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION protect_promotion_migration_recovery() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' THEN
  IF EXISTS(SELECT 1 FROM project_environment_promotions WHERE id=OLD.promotion_id) THEN
   RAISE EXCEPTION 'promotion recovery follows parent lifetime' USING ERRCODE='23514';
  END IF;
  RETURN OLD;
 END IF;
 IF OLD.status IN ('resumed','resolved','blocked') OR NEW.request_hash<>OLD.request_hash OR NEW.payload#>'{receipt,request}' IS DISTINCT FROM OLD.payload#>'{receipt,request}'
  OR NEW.payload#>'{receipt,operator_account_id}' IS DISTINCT FROM OLD.payload#>'{receipt,operator_account_id}' OR NEW.payload#>'{receipt,requested_at}' IS DISTINCT FROM OLD.payload#>'{receipt,requested_at}'
  OR NEW.payload#>'{receipt,deadline}' IS DISTINCT FROM OLD.payload#>'{receipt,deadline}' OR NEW.payload->'runtime_fingerprint' IS DISTINCT FROM OLD.payload->'runtime_fingerprint' THEN
  RAISE EXCEPTION 'promotion recovery identity and terminal decisions are immutable' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE OR REPLACE TRIGGER immutable_promotion_migration_recovery BEFORE DELETE OR UPDATE ON project_environment_promotion_migration_recoveries FOR EACH ROW EXECUTE FUNCTION protect_promotion_migration_recovery();
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION protect_promotion_migration_runtime() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF OLD.destroyed_at IS NOT NULL AND NEW.destroyed_at IS DISTINCT FROM OLD.destroyed_at OR
  EXISTS(SELECT 1 FROM app_tasks WHERE id=OLD.task_id AND started_at IS NOT NULL) AND
  (NEW.runtime_node_id IS DISTINCT FROM OLD.runtime_node_id OR NEW.runtime_instance_id IS DISTINCT FROM OLD.runtime_instance_id OR NEW.runtime_lease_token IS DISTINCT FROM OLD.runtime_lease_token OR NEW.prepared_lease_token IS DISTINCT FROM OLD.prepared_lease_token) THEN
  RAISE EXCEPTION 'dispatched migration runtime identity is immutable' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE OR REPLACE TRIGGER immutable_promotion_migration_runtime BEFORE UPDATE ON project_environment_promotion_migrations FOR EACH ROW EXECUTE FUNCTION protect_promotion_migration_runtime();
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION protect_promotion_health_status() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE health_phase text;
BEGIN
 SELECT phase INTO health_phase FROM project_environment_promotion_health WHERE promotion_id=OLD.id;
 IF health_phase IS NOT NULL AND (
  (NEW.status='running' AND OLD.status<>'running' AND NOT (OLD.status='failed' AND health_phase='preflight' AND EXISTS(SELECT 1 FROM project_environment_promotion_migration_recoveries WHERE promotion_id=OLD.id AND status='resumed'))) OR
  (NEW.status='succeeded' AND health_phase<>'succeeded') OR
  (NEW.status='failed' AND health_phase NOT IN ('preflight','failed','conflicted','rollback_failed'))
 ) THEN RAISE EXCEPTION 'promotion status is owned by destination health' USING ERRCODE='23514'; END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE OR REPLACE VIEW project_promotion_migration_resolved_tasks AS
 SELECT m.task_id FROM project_environment_promotion_migrations m
 JOIN project_environment_promotion_migration_recoveries r ON r.promotion_id=m.promotion_id
 WHERE r.status IN ('resumed','resolved') AND m.destroyed_at IS NOT NULL AND m.runtime_lease_token=m.prepared_lease_token;
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_promotion_migration_task() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE project uuid; owner record;
BEGIN
 IF NEW.kind<>'release' THEN RETURN NEW; END IF;
 SELECT project_id INTO project FROM apps WHERE id=NEW.app_id;
 IF project IS NULL THEN RETURN NEW; END IF;
 PERFORM 1 FROM projects WHERE id=project FOR UPDATE;
 IF EXISTS(SELECT 1 FROM project_environment_promotion_migrations m JOIN project_environment_promotions p ON p.id=m.promotion_id
   WHERE p.project_id=project AND (p.status='running' OR EXISTS(SELECT 1 FROM project_environment_promotion_migration_recoveries recovery WHERE recovery.promotion_id=p.id AND recovery.status IN ('quiescing','verifying')) OR EXISTS(SELECT 1 FROM app_tasks previous WHERE previous.id=m.task_id AND previous.started_at IS NOT NULL AND previous.exit_code IS NULL AND NOT EXISTS(SELECT 1 FROM project_promotion_migration_resolved_tasks resolved WHERE resolved.task_id=previous.id))) AND m.task_id IS DISTINCT FROM NEW.id) AND (TG_OP='INSERT' OR NEW.status IN ('restoring','running')) THEN
  RAISE EXCEPTION 'project has an admitted promotion migration' USING ERRCODE='23514';
 END IF;
 SELECT p.status,h.phase,h.payload,m.prepared_lease_token,m.runtime_lease_token,m.runtime_node_id,m.runtime_instance_id INTO owner FROM project_environment_promotion_migrations m
 JOIN project_environment_promotions p ON p.id=m.promotion_id JOIN project_environment_promotion_health h ON h.promotion_id=p.id WHERE m.task_id=NEW.id;
 IF FOUND AND NEW.status IN ('restoring','running') THEN
  IF owner.status<>'running' OR owner.phase<>'migrating' OR (owner.payload->>'deadline')::timestamptz<=clock_timestamp()
    OR NEW.retry_max<>0 OR (NEW.status='running' AND (owner.prepared_lease_token IS NULL OR owner.prepared_lease_token IS DISTINCT FROM NEW.lease_token OR owner.runtime_lease_token IS DISTINCT FROM NEW.lease_token OR owner.runtime_node_id IS NULL OR owner.runtime_instance_id IS NULL)) THEN
   RAISE EXCEPTION 'promotion migration is not authorized for dispatch' USING ERRCODE='23514';
  END IF;
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM project_environment_promotion_migration_recoveries) OR EXISTS(SELECT 1 FROM project_environment_promotion_migrations WHERE runtime_node_id IS NOT NULL) THEN RAISE EXCEPTION 'cannot discard promotion recovery evidence'; END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_promotion_migration_task() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE project uuid; owner record;
BEGIN
 IF NEW.kind<>'release' THEN RETURN NEW; END IF;
 SELECT project_id INTO project FROM apps WHERE id=NEW.app_id;
 IF project IS NULL THEN RETURN NEW; END IF;
 PERFORM 1 FROM projects WHERE id=project FOR UPDATE;
 IF EXISTS(SELECT 1 FROM project_environment_promotion_migrations m JOIN project_environment_promotions p ON p.id=m.promotion_id
   WHERE p.project_id=project AND (p.status='running' OR EXISTS(SELECT 1 FROM app_tasks previous WHERE previous.id=m.task_id AND previous.started_at IS NOT NULL AND previous.exit_code IS NULL)) AND m.task_id IS DISTINCT FROM NEW.id) AND (TG_OP='INSERT' OR NEW.status IN ('restoring','running')) THEN
  RAISE EXCEPTION 'project has an admitted promotion migration' USING ERRCODE='23514';
 END IF;
 SELECT p.status,h.phase,h.payload,m.prepared_lease_token INTO owner FROM project_environment_promotion_migrations m
 JOIN project_environment_promotions p ON p.id=m.promotion_id JOIN project_environment_promotion_health h ON h.promotion_id=p.id WHERE m.task_id=NEW.id;
 IF FOUND AND NEW.status IN ('restoring','running') THEN
  IF owner.status<>'running' OR owner.phase<>'migrating' OR (owner.payload->>'deadline')::timestamptz<=clock_timestamp()
    OR NEW.retry_max<>0 OR (NEW.status='running' AND (owner.prepared_lease_token IS NULL OR owner.prepared_lease_token IS DISTINCT FROM NEW.lease_token)) THEN
   RAISE EXCEPTION 'promotion migration is not authorized for dispatch' USING ERRCODE='23514';
  END IF;
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION protect_promotion_health_status() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE health_phase text;
BEGIN
 SELECT phase INTO health_phase FROM project_environment_promotion_health WHERE promotion_id=OLD.id;
 IF health_phase IS NOT NULL AND (
  (NEW.status='running' AND OLD.status<>'running') OR
  (NEW.status='succeeded' AND health_phase<>'succeeded') OR
  (NEW.status='failed' AND health_phase NOT IN ('preflight','failed','conflicted','rollback_failed'))
 ) THEN
  RAISE EXCEPTION 'promotion status is owned by destination health' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
DROP VIEW project_promotion_migration_resolved_tasks;
DROP TRIGGER immutable_promotion_migration_runtime ON project_environment_promotion_migrations;
DROP FUNCTION protect_promotion_migration_runtime();
DROP TABLE project_environment_promotion_migration_recoveries;
DROP FUNCTION protect_promotion_migration_recovery();
ALTER TABLE project_environment_promotion_migrations DROP CONSTRAINT promotion_migration_runtime_complete,DROP COLUMN destroyed_at,DROP COLUMN runtime_lease_token,DROP COLUMN runtime_instance_id,DROP COLUMN runtime_node_id;
