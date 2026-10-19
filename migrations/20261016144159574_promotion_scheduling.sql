-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE project_environment_promotion_health
 ADD COLUMN IF NOT EXISTS next_check_at timestamptz NOT NULL DEFAULT now() CHECK (isfinite(next_check_at)),
 ADD COLUMN IF NOT EXISTS last_claimed_at timestamptz NOT NULL DEFAULT '-infinity' CHECK (isfinite(last_claimed_at) OR last_claimed_at='-infinity'::timestamptz);
CREATE INDEX IF NOT EXISTS promotion_health_due ON project_environment_promotion_health(next_check_at,last_claimed_at,promotion_id);
CREATE INDEX IF NOT EXISTS promotion_health_leased ON project_environment_promotion_health(lease_until) WHERE lease_token IS NOT NULL;
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION schedule_promotion_health() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.lease_token IS DISTINCT FROM OLD.lease_token AND NEW.lease_token IS NOT NULL THEN
  NEW.last_claimed_at := clock_timestamp();
  NEW.next_check_at := NEW.lease_until;
 ELSIF NEW.generation<>OLD.generation THEN
  NEW.next_check_at := least(NEW.next_check_at,clock_timestamp());
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE OR REPLACE TRIGGER promotion_health_schedule BEFORE UPDATE ON project_environment_promotion_health FOR EACH ROW EXECUTE FUNCTION schedule_promotion_health();
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION wake_promotion_recovery() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 UPDATE project_environment_promotion_health SET next_check_at=least(next_check_at,clock_timestamp()) WHERE promotion_id=NEW.promotion_id;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE OR REPLACE TRIGGER promotion_recovery_schedule AFTER INSERT OR UPDATE OF status ON project_environment_promotion_migration_recoveries FOR EACH ROW EXECUTE FUNCTION wake_promotion_recovery();
CREATE OR REPLACE VIEW project_promotion_health_work AS
 SELECT p.id promotion_id,p.account_id,p.project_slug,p.to_environment,h.next_check_at,h.last_claimed_at,h.lease_until,
 (h.phase IN ('restoring','rolling_back') OR EXISTS(SELECT 1 FROM project_environment_promotion_migration_recoveries r WHERE r.promotion_id=p.id AND r.status IN ('quiescing','verifying'))) urgent
 FROM project_environment_promotions p JOIN project_environment_promotion_health h ON h.promotion_id=p.id
 WHERE p.status='running' AND h.phase IN ('migrating','preflight','cutover','verifying','finalizing','restoring','rolling_back')
 OR p.status='failed' AND EXISTS(SELECT 1 FROM project_environment_promotion_migration_recoveries r WHERE r.promotion_id=p.id AND r.status IN ('quiescing','verifying'));

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM project_environment_promotion_health WHERE lease_until>clock_timestamp()) THEN RAISE EXCEPTION 'cannot remove promotion scheduling with active leases'; END IF;
END $$;
-- +goose StatementEnd
DROP VIEW project_promotion_health_work;
DROP TRIGGER promotion_recovery_schedule ON project_environment_promotion_migration_recoveries;
DROP FUNCTION wake_promotion_recovery();
DROP TRIGGER promotion_health_schedule ON project_environment_promotion_health;
DROP FUNCTION schedule_promotion_health();
DROP INDEX promotion_health_leased;
DROP INDEX promotion_health_due;
ALTER TABLE project_environment_promotion_health DROP COLUMN next_check_at,DROP COLUMN last_claimed_at;
