-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';

-- Start the service readiness budget when the artifact becomes runnable.
-- N-1 writers and repeated LIVE notifications share this transition fence.
-- +goose StatementBegin
DO $install$
BEGIN
 IF to_regprocedure('start_service_rollout_readiness_clock()') IS NULL THEN
 EXECUTE $function$
CREATE FUNCTION start_service_rollout_readiness_clock() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
 IF OLD.status <> 'live' AND NEW.status = 'live'
    AND NEW.canary_total_steps = 0 AND NEW.rollout_state = 'rolling_out' THEN
  NEW.rollout_started_at := clock_timestamp();
 END IF;
 RETURN NEW;
END $$;
$function$;
 END IF;
END;
$install$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $install$
BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid='deployments'::regclass AND tgname='deployments_service_readiness_clock') THEN
CREATE TRIGGER deployments_service_readiness_clock
BEFORE UPDATE OF status ON deployments
FOR EACH ROW EXECUTE FUNCTION start_service_rollout_readiness_clock();
 END IF;
END;
$install$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TRIGGER IF EXISTS deployments_service_readiness_clock ON deployments;
DROP FUNCTION IF EXISTS start_service_rollout_readiness_clock();
