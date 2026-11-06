-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';

-- Start the service readiness budget when the artifact becomes runnable.
-- N-1 writers and repeated LIVE notifications share this transition fence.
-- +goose StatementBegin
CREATE FUNCTION start_service_rollout_readiness_clock() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
 IF OLD.status <> 'live' AND NEW.status = 'live'
    AND NEW.canary_total_steps = 0 AND NEW.rollout_state = 'rolling_out' THEN
  NEW.rollout_started_at := clock_timestamp();
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER deployments_service_readiness_clock
BEFORE UPDATE OF status ON deployments
FOR EACH ROW EXECUTE FUNCTION start_service_rollout_readiness_clock();

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TRIGGER IF EXISTS deployments_service_readiness_clock ON deployments;
DROP FUNCTION IF EXISTS start_service_rollout_readiness_clock();
