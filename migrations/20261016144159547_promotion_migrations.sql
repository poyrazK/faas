-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
-- Preserve a later phase expansion when this migration's ledger row is replayed.
-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='project_environment_promotion_health'::regclass AND conname='project_environment_promotion_health_phase_check' AND strpos(pg_get_constraintdef(oid), '''migrating''') > 0) THEN
  ALTER TABLE project_environment_promotion_health DROP CONSTRAINT IF EXISTS project_environment_promotion_health_phase_check;
  ALTER TABLE project_environment_promotion_health ADD CONSTRAINT project_environment_promotion_health_phase_check CHECK (phase IN ('migrating','preflight','cutover','verifying','finalizing','rolling_back','succeeded','failed','conflicted','rollback_failed'));
 END IF;
END $$;
-- +goose StatementEnd
CREATE TABLE IF NOT EXISTS project_environment_promotion_migrations (
 promotion_id uuid PRIMARY KEY REFERENCES project_environment_promotions(id) ON DELETE CASCADE,
 task_id uuid UNIQUE REFERENCES app_tasks(id) DEFERRABLE INITIALLY DEFERRED,
 runtime_fingerprint text NOT NULL DEFAULT '' CHECK (runtime_fingerprint='' OR runtime_fingerprint ~ '^[a-f0-9]{64}$'),
 prepared_lease_token uuid,
 CHECK ((task_id IS NULL)=(runtime_fingerprint=''))
);
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION protect_promotion_migration_capture() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' THEN
  IF EXISTS(SELECT 1 FROM project_environment_promotions WHERE id=OLD.promotion_id) THEN
   RAISE EXCEPTION 'promotion migration evidence follows promotion lifetime' USING ERRCODE='23514';
  END IF;
  RETURN OLD;
 END IF;
 IF OLD.task_id IS NOT NULL AND (NEW.task_id IS DISTINCT FROM OLD.task_id OR NEW.runtime_fingerprint<>OLD.runtime_fingerprint) THEN
  RAISE EXCEPTION 'promotion migration task and runtime identity are immutable' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE OR REPLACE TRIGGER immutable_promotion_migration BEFORE DELETE OR UPDATE ON project_environment_promotion_migrations FOR EACH ROW EXECUTE FUNCTION protect_promotion_migration_capture();
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
CREATE OR REPLACE TRIGGER promotion_migration_task_gate BEFORE INSERT OR UPDATE OF status ON app_tasks FOR EACH ROW EXECUTE FUNCTION guard_promotion_migration_task();
DROP INDEX IF EXISTS project_environment_promotion_health_pending;
CREATE INDEX IF NOT EXISTS project_environment_promotion_health_pending ON project_environment_promotion_health(promotion_id) WHERE phase IN ('migrating','preflight','cutover','verifying','finalizing','rolling_back');

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM project_environment_promotion_migrations) THEN
  RAISE EXCEPTION 'cannot discard admitted promotion migration evidence';
 END IF;
END $$;
-- +goose StatementEnd
DROP TRIGGER promotion_migration_task_gate ON app_tasks;
DROP FUNCTION guard_promotion_migration_task();
DROP TABLE project_environment_promotion_migrations;
DROP FUNCTION protect_promotion_migration_capture();
DROP INDEX project_environment_promotion_health_pending;
CREATE INDEX project_environment_promotion_health_pending ON project_environment_promotion_health(promotion_id) WHERE phase IN ('preflight','cutover','verifying','finalizing','rolling_back');
UPDATE project_environment_promotion_health SET phase='failed',payload=jsonb_set(payload,'{phase}','"failed"') WHERE phase='migrating';
ALTER TABLE project_environment_promotion_health DROP CONSTRAINT project_environment_promotion_health_phase_check;
ALTER TABLE project_environment_promotion_health ADD CONSTRAINT project_environment_promotion_health_phase_check CHECK (phase IN ('preflight','cutover','verifying','finalizing','rolling_back','succeeded','failed','conflicted','rollback_failed'));
