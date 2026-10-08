-- filename: 20261008114924464_runtime_upgrade_reservation_controls.sql

-- +goose Up
-- ADR-692: reserve before source I/O, with private status/cancel controls.
ALTER TABLE runtime_upgrade_operations ADD COLUMN source_path text NOT NULL DEFAULT '';
ALTER TABLE runtime_upgrade_operations DROP CONSTRAINT runtime_upgrade_operations_phase_check;
ALTER TABLE runtime_upgrade_operations DROP CONSTRAINT runtime_upgrade_operations_check3;
ALTER TABLE runtime_upgrade_operations ADD CONSTRAINT runtime_upgrade_operations_phase_check
 CHECK (phase IN ('reserved','prepared','waiting','complete','blocked','cancelled'));
ALTER TABLE runtime_upgrade_operations ADD CONSTRAINT runtime_upgrade_operations_state_check CHECK (
 (phase IN ('reserved','prepared','waiting') AND blocker='' AND wake_id IS NULL AND finished_at IS NULL)
 OR (phase='complete' AND blocker='' AND wake_id IS NOT NULL AND finished_at IS NOT NULL AND lease_token IS NULL)
 OR (phase='blocked' AND blocker<>'' AND wake_id IS NULL AND finished_at IS NOT NULL AND lease_token IS NULL)
 OR (phase='cancelled' AND blocker='' AND wake_id IS NULL AND finished_at IS NOT NULL AND lease_token IS NULL));
ALTER TABLE runtime_upgrade_operations ADD CONSTRAINT runtime_upgrade_operations_reservation_path_check
 CHECK (phase<>'reserved' OR (source_path LIKE '/%' AND length(source_path)>1));
DROP INDEX runtime_upgrade_operations_active_app;
CREATE UNIQUE INDEX runtime_upgrade_operations_active_app ON runtime_upgrade_operations(app_id) WHERE phase IN ('reserved','prepared','waiting');
DROP INDEX runtime_upgrade_operations_due;
CREATE INDEX runtime_upgrade_operations_due ON runtime_upgrade_operations(next_attempt_at,created_at,id) WHERE phase IN ('reserved','prepared','waiting');

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_runtime_upgrade_operation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='UPDATE' THEN
  IF (NEW.id,NEW.account_id,NEW.app_id,NEW.deployment_id,NEW.serving_deployment_id,NEW.target_release_id,
      NEW.source_sha256,NEW.qualification_report_sha256,NEW.created_at,NEW.deadline_at,NEW.source_path)
   IS DISTINCT FROM
     (OLD.id,OLD.account_id,OLD.app_id,OLD.deployment_id,OLD.serving_deployment_id,OLD.target_release_id,
      OLD.source_sha256,OLD.qualification_report_sha256,OLD.created_at,OLD.deadline_at,OLD.source_path)
   OR OLD.phase IN ('complete','blocked','cancelled')
   OR (OLD.phase='waiting' AND NEW.phase IN ('reserved','prepared'))
   OR (OLD.phase='prepared' AND NEW.phase='reserved')
   OR (OLD.phase='reserved' AND NEW.phase IN ('waiting','complete')) THEN
   RAISE EXCEPTION 'immutable runtime upgrade intent or terminal operation' USING ERRCODE='23514';
  END IF;
 END IF;
 IF NEW.phase='complete' AND NOT EXISTS (
  SELECT 1 FROM deployment_runtime_upgrade_cutovers c WHERE c.deployment_id=NEW.deployment_id
   AND c.serving_deployment_id=NEW.serving_deployment_id AND c.target_release_id=NEW.target_release_id
   AND c.qualification_report_sha256=NEW.qualification_report_sha256 AND c.wake_id=NEW.wake_id
 ) THEN
  RAISE EXCEPTION 'runtime upgrade completion requires retained cutover' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd

-- Existing owner fences serialize generic build/cutover writers with cancellation.
-- +goose StatementBegin
CREATE FUNCTION guard_runtime_upgrade_operation_effect() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE operation_phase text;
BEGIN
 PERFORM 1 FROM apps a JOIN deployments d ON d.app_id=a.id WHERE d.id=NEW.deployment_id FOR UPDATE OF a;
 SELECT phase INTO operation_phase FROM runtime_upgrade_operations WHERE deployment_id=NEW.deployment_id;
 IF (TG_TABLE_NAME='builds' AND operation_phase IN ('reserved','blocked','cancelled','complete'))
  OR (TG_TABLE_NAME='deployment_runtime_upgrade_cutovers' AND operation_phase IS NOT NULL AND operation_phase<>'waiting') THEN
  RAISE EXCEPTION 'runtime upgrade operation cannot publish this effect' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER runtime_upgrade_build_admission_guard BEFORE INSERT ON builds FOR EACH ROW EXECUTE FUNCTION guard_runtime_upgrade_operation_effect();
CREATE TRIGGER runtime_upgrade_cutover_operation_guard BEFORE INSERT ON deployment_runtime_upgrade_cutovers FOR EACH ROW EXECUTE FUNCTION guard_runtime_upgrade_operation_effect();

-- +goose Down
-- Forward-only: cancellation/reservation history cannot be erased on rollback.
SELECT 1;
