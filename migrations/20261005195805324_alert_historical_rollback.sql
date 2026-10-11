-- +goose Up
ALTER TABLE alert_rules ADD COLUMN IF NOT EXISTS post_deploy_rollback_window_seconds integer NOT NULL DEFAULT 0
 CHECK(post_deploy_rollback_window_seconds BETWEEN 0 AND 3600);
-- +goose StatementBegin
DO $$
BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='alert_rules'::regclass
  AND conname='alert_rules_historical_rollback_chk') THEN
  ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_historical_rollback_chk CHECK
   (post_deploy_rollback_window_seconds=0 OR (action='rollback' AND app_id IS NOT NULL));
 END IF;
END $$;
-- +goose StatementEnd
CREATE TABLE IF NOT EXISTS deployment_recovery_lineage (
 deployment_id uuid PRIMARY KEY REFERENCES deployments(id) ON DELETE CASCADE,
 predecessor_deployment_id uuid REFERENCES deployments(id) ON DELETE SET NULL,
 CHECK(deployment_id<>predecessor_deployment_id)
);
CREATE TABLE IF NOT EXISTS alert_historical_rollback_claims (
 deployment_id uuid PRIMARY KEY REFERENCES deployments(id) ON DELETE CASCADE,
 fire_id uuid NOT NULL
);
-- No backfill: old release provenance is unknown. Rule deletion must not rearm a release.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION capture_deployment_recovery_lineage() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE predecessor uuid;
BEGIN
 PERFORM id FROM apps WHERE id=NEW.app_id FOR UPDATE;
 IF (SELECT count(*) FROM deployments d WHERE d.app_id=NEW.app_id AND d.scope=NEW.scope AND d.id<>NEW.id
  AND d.status='live' AND d.traffic_percent>0 AND d.deleted_at IS NULL)=1 THEN
  SELECT id INTO predecessor FROM deployments d WHERE d.app_id=NEW.app_id AND d.scope=NEW.scope AND d.id<>NEW.id
   AND d.status='live' AND d.traffic_percent=100 AND d.rollout_state='complete' AND d.deleted_at IS NULL
   AND (d.canary_total_steps=0 OR d.canary_step>=d.canary_total_steps);
  IF predecessor IS NOT NULL THEN
   INSERT INTO deployment_recovery_lineage(deployment_id,predecessor_deployment_id) VALUES(NEW.id,predecessor);
  END IF;
 END IF;
 RETURN NEW;
END $$;
CREATE OR REPLACE TRIGGER deployment_recovery_lineage_capture AFTER INSERT ON deployments FOR EACH ROW
 EXECUTE FUNCTION capture_deployment_recovery_lineage();
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER deployment_recovery_lineage_capture ON deployments;
DROP FUNCTION capture_deployment_recovery_lineage();
DROP TABLE alert_historical_rollback_claims;
DROP TABLE deployment_recovery_lineage;
ALTER TABLE alert_rules DROP CONSTRAINT alert_rules_historical_rollback_chk;
ALTER TABLE alert_rules DROP COLUMN post_deploy_rollback_window_seconds;
