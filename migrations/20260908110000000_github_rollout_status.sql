-- +goose Up
-- +goose StatementBegin
-- A GitHub Deployment must follow safe-release transitions as well as the
-- build status. Canary rows remain `live` while their rollout_state and
-- traffic share change, so status-only notifications leave GitHub showing a
-- false success until the next build transition. Keep the existing durable
-- coalescing outbox, but enqueue it for every customer-visible rollout step.
CREATE OR REPLACE FUNCTION notify_github_deployment_status_changed()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF (TG_OP = 'INSERT'
      OR NEW.status IS DISTINCT FROM OLD.status
      OR NEW.rollout_state IS DISTINCT FROM OLD.rollout_state
      OR NEW.canary_step IS DISTINCT FROM OLD.canary_step
      OR NEW.traffic_percent IS DISTINCT FROM OLD.traffic_percent)
     AND NEW.kind IN ('github', 'preview') THEN
    INSERT INTO github_check_updates (deployment_id)
    VALUES (NEW.id)
    ON CONFLICT (deployment_id) DO UPDATE SET
      generation = github_check_updates.generation + 1,
      status = 'pending',
      attempts = 0,
      next_attempt_at = now(),
      last_error = '',
      processed_at = NULL,
      updated_at = now();

    PERFORM pg_notify('github_deployment_changed', json_build_object(
      'kind', NEW.kind,
      'app_id', NEW.app_id,
      'deployment_id', NEW.id,
      'status', NEW.status,
      'rollout_state', NEW.rollout_state,
      'canary_step', NEW.canary_step,
      'traffic_percent', NEW.traffic_percent
    )::text);
  END IF;
  RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS github_deployment_status_changed_trg ON deployments;
CREATE TRIGGER github_deployment_status_changed_trg
AFTER INSERT OR UPDATE OF status, rollout_state, canary_step, traffic_percent ON deployments
FOR EACH ROW EXECUTE FUNCTION notify_github_deployment_status_changed();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION notify_github_deployment_status_changed()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF (TG_OP = 'INSERT' OR NEW.status IS DISTINCT FROM OLD.status)
     AND NEW.kind IN ('github', 'preview') THEN
    INSERT INTO github_check_updates (deployment_id)
    VALUES (NEW.id)
    ON CONFLICT (deployment_id) DO UPDATE SET
      generation = github_check_updates.generation + 1,
      status = 'pending',
      attempts = 0,
      next_attempt_at = now(),
      last_error = '',
      processed_at = NULL,
      updated_at = now();
    PERFORM pg_notify('github_deployment_changed', json_build_object(
      'kind', NEW.kind,
      'app_id', NEW.app_id,
      'deployment_id', NEW.id,
      'status', NEW.status
    )::text);
  END IF;
  RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS github_deployment_status_changed_trg ON deployments;
CREATE TRIGGER github_deployment_status_changed_trg
AFTER INSERT OR UPDATE OF status ON deployments
FOR EACH ROW EXECUTE FUNCTION notify_github_deployment_status_changed();
-- +goose StatementEnd
