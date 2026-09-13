-- +goose Up
-- A terminally failed deployment must never retain customer traffic or an
-- in-progress rollout. Repair legacy rows before installing the invariant.
-- The trigger keeps older apid/imaged binaries compatible during a rolling
-- deploy while enforcing the same transition at the database boundary.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION enforce_failed_deployment_traffic_fence() RETURNS trigger AS $$
BEGIN
    IF NEW.status = 'failed' THEN
        NEW.traffic_percent := 0;
        NEW.rollout_state := 'aborted';
        NEW.rollout_completed_at := NULL;
        NEW.rollout_aborted_at := COALESCE(NEW.rollout_aborted_at, now());
        NEW.rollout_aborted_reason := COALESCE(NULLIF(NEW.rollout_aborted_reason, ''), NULLIF(NEW.error, ''), 'deployment failed');
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

DROP TRIGGER IF EXISTS deployments_failed_traffic_fence ON deployments;
CREATE TRIGGER deployments_failed_traffic_fence
BEFORE INSERT OR UPDATE OF status, traffic_percent, rollout_state, rollout_aborted_at ON deployments
FOR EACH ROW EXECUTE FUNCTION enforce_failed_deployment_traffic_fence();

UPDATE deployments
SET traffic_percent = 0,
    rollout_state = 'aborted',
    rollout_completed_at = NULL,
    rollout_aborted_at = COALESCE(rollout_aborted_at, now()),
    rollout_aborted_reason = COALESCE(NULLIF(rollout_aborted_reason, ''), NULLIF(error, ''), 'deployment failed')
WHERE status = 'failed';

ALTER TABLE deployments
    DROP CONSTRAINT IF EXISTS deployments_failed_traffic_fence_chk;
ALTER TABLE deployments
    ADD CONSTRAINT deployments_failed_traffic_fence_chk CHECK (
        status <> 'failed' OR (
            traffic_percent = 0 AND
            rollout_state = 'aborted' AND
            rollout_aborted_at IS NOT NULL
        )
    );

-- +goose Down
ALTER TABLE deployments
    DROP CONSTRAINT IF EXISTS deployments_failed_traffic_fence_chk;
DROP TRIGGER IF EXISTS deployments_failed_traffic_fence ON deployments;
DROP FUNCTION IF EXISTS enforce_failed_deployment_traffic_fence();
