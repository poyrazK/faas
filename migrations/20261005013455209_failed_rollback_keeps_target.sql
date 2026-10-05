-- filename: 20261005013455209_failed_rollback_keeps_target.sql

-- A rollback re-primes an earlier release: PrepareDeploymentRollback moves a
-- superseded (or demoted live 0%) deployment to snapshotting, and schedd and
-- imaged then boot, snapshot and verify it. Any failure in that pipeline
-- marked the deployment failed, which removes it as a rollback target for
-- good, even when the cause was transient. On production-us a rollback
-- target hit a 429 storm from the old stable deployment, failed hosting
-- verification after nine minutes, and the release was lost.
--
-- A deployment that served before keeps serving_ended_at (migration
-- 20261004234807528) through the re-prime, and a never-served deployment has
-- none. So a failure of a snapshotting deployment with that stamp returns it
-- to superseded, with the error kept for diagnosis. A first deploy still
-- fails normally.

-- +goose Up
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION keep_failed_rollback_target() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.status = 'failed' AND OLD.status = 'snapshotting' AND OLD.serving_ended_at IS NOT NULL THEN
  NEW.status := 'superseded';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS deployment_failed_rollback_keeps_target ON deployments;
CREATE TRIGGER deployment_failed_rollback_keeps_target BEFORE UPDATE OF status ON deployments
 FOR EACH ROW EXECUTE FUNCTION keep_failed_rollback_target();

-- +goose Down
DROP TRIGGER IF EXISTS deployment_failed_rollback_keeps_target ON deployments;
DROP FUNCTION IF EXISTS keep_failed_rollback_target();
