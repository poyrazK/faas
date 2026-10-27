-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='project_environment_promotion_health'::regclass AND conname='project_environment_promotion_health_phase_check' AND strpos(pg_get_constraintdef(oid), '''queued''') > 0) THEN
  ALTER TABLE project_environment_promotion_health DROP CONSTRAINT IF EXISTS project_environment_promotion_health_phase_check;
  ALTER TABLE project_environment_promotion_health ADD CONSTRAINT project_environment_promotion_health_phase_check CHECK (phase IN ('queued','migrating','preflight','cutover','verifying','finalizing','rolling_back','restoring','aborted','succeeded','failed','conflicted','rollback_failed'));
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='project_environment_promotion_health'::regclass AND conname='promotion_admission_payload') THEN
ALTER TABLE project_environment_promotion_health ADD CONSTRAINT promotion_admission_payload CHECK (
 NOT (payload ? 'admission') OR (jsonb_typeof(payload->'admission')='object'
 AND (payload->'admission') ?& ARRAY['queued_at','wait_deadline','reason']
 AND isfinite((payload#>>'{admission,queued_at}')::timestamptz)
 AND isfinite((payload#>>'{admission,wait_deadline}')::timestamptz)
 AND (payload#>>'{admission,wait_deadline}')::timestamptz>(payload#>>'{admission,queued_at}')::timestamptz
 AND (payload#>>'{admission,started_at}' IS NULL OR isfinite((payload#>>'{admission,started_at}')::timestamptz))) IS TRUE);
 END IF;
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='project_environment_promotion_health'::regclass AND conname='promotion_queued_payload') THEN
ALTER TABLE project_environment_promotion_health ADD CONSTRAINT promotion_queued_payload CHECK (
 phase<>'queued' OR (payload ? 'admission' AND payload#>>'{admission,started_at}' IS NULL
 AND payload->>'started_at'=payload#>>'{admission,queued_at}' AND payload->>'deadline'=payload#>>'{admission,wait_deadline}') IS TRUE);
 END IF;
END $$;
-- +goose StatementEnd
CREATE INDEX IF NOT EXISTS promotion_admission_inventory ON project_environment_promotion_health(phase,promotion_id);
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION protect_promotion_admission() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF OLD.payload ? 'admission' AND (
  NOT NEW.payload ? 'admission'
  OR NEW.payload#>'{admission,queued_at}' IS DISTINCT FROM OLD.payload#>'{admission,queued_at}'
  OR NEW.payload#>'{admission,wait_deadline}' IS DISTINCT FROM OLD.payload#>'{admission,wait_deadline}'
  OR (OLD.payload#>>'{admission,started_at}' IS NOT NULL AND NEW.payload#>'{admission,started_at}' IS DISTINCT FROM OLD.payload#>'{admission,started_at}')
 ) THEN RAISE EXCEPTION 'promotion queue identity and execution start are immutable' USING ERRCODE='23514'; END IF;
 IF NEW.phase='queued' AND OLD.phase<>'queued' THEN RAISE EXCEPTION 'active promotion cannot reenter waiting queue' USING ERRCODE='23514'; END IF;
 IF OLD.phase='queued' AND NEW.phase IN ('preflight','migrating') THEN
  IF coalesce(current_setting('faas.promotion_queue_dispatch',true),'')<>OLD.promotion_id::text || ':' || OLD.generation::text || ':' || coalesce(OLD.lease_token::text,'')
   OR OLD.lease_token IS NULL OR OLD.lease_until<=clock_timestamp()
   OR (OLD.payload#>>'{admission,wait_deadline}')::timestamptz<=clock_timestamp()
   OR NEW.payload#>>'{admission,started_at}' IS NULL
   OR NEW.payload#>>'{admission,started_at}' IS DISTINCT FROM NEW.payload->>'started_at'
   OR NEW.generation<>OLD.generation+1
  THEN RAISE EXCEPTION 'queued promotion requires validated dispatch ownership' USING ERRCODE='23514'; END IF;
 ELSIF OLD.phase='queued' AND NEW.phase NOT IN ('queued','failed','conflicted','aborted') THEN
  RAISE EXCEPTION 'queued promotion cannot publish or skip preflight' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE OR REPLACE TRIGGER promotion_admission_gate BEFORE UPDATE ON project_environment_promotion_health FOR EACH ROW EXECUTE FUNCTION protect_promotion_admission();
CREATE OR REPLACE VIEW project_promotion_health_work AS
 SELECT p.id promotion_id,p.account_id,p.project_slug,p.to_environment,h.next_check_at,h.last_claimed_at,h.lease_until,
 (h.phase IN ('restoring','rolling_back') OR EXISTS(SELECT 1 FROM project_environment_promotion_migration_recoveries r WHERE r.promotion_id=p.id AND r.status IN ('quiescing','verifying'))) urgent
 FROM project_environment_promotions p JOIN project_environment_promotion_health h ON h.promotion_id=p.id
 WHERE p.status='running' AND h.phase IN ('queued','migrating','preflight','cutover','verifying','finalizing','restoring','rolling_back')
 OR p.status='failed' AND EXISTS(SELECT 1 FROM project_environment_promotion_migration_recoveries r WHERE r.promotion_id=p.id AND r.status IN ('quiescing','verifying'));

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM project_environment_promotion_health WHERE payload ? 'admission' OR lease_until>clock_timestamp()) THEN RAISE EXCEPTION 'cannot remove admission fencing while queue receipts or active leases exist'; END IF;
END $$;
-- +goose StatementEnd
CREATE OR REPLACE VIEW project_promotion_health_work AS
 SELECT p.id promotion_id,p.account_id,p.project_slug,p.to_environment,h.next_check_at,h.last_claimed_at,h.lease_until,
 (h.phase IN ('restoring','rolling_back') OR EXISTS(SELECT 1 FROM project_environment_promotion_migration_recoveries r WHERE r.promotion_id=p.id AND r.status IN ('quiescing','verifying'))) urgent
 FROM project_environment_promotions p JOIN project_environment_promotion_health h ON h.promotion_id=p.id
 WHERE p.status='running' AND h.phase IN ('migrating','preflight','cutover','verifying','finalizing','restoring','rolling_back')
 OR p.status='failed' AND EXISTS(SELECT 1 FROM project_environment_promotion_migration_recoveries r WHERE r.promotion_id=p.id AND r.status IN ('quiescing','verifying'));
DROP TRIGGER promotion_admission_gate ON project_environment_promotion_health;
DROP FUNCTION protect_promotion_admission();
DROP INDEX promotion_admission_inventory;
ALTER TABLE project_environment_promotion_health DROP CONSTRAINT promotion_admission_payload,DROP CONSTRAINT promotion_queued_payload,DROP CONSTRAINT project_environment_promotion_health_phase_check;
ALTER TABLE project_environment_promotion_health ADD CONSTRAINT project_environment_promotion_health_phase_check CHECK (phase IN ('migrating','preflight','cutover','verifying','finalizing','rolling_back','restoring','aborted','succeeded','failed','conflicted','rollback_failed'));
