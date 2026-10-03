-- +goose Up
-- +goose StatementBegin
-- The deployment status and rollout state are separate: live means ready to
-- serve, while complete/aborted is the final release outcome. Enqueue inside
-- the status mutation transaction so a committed outcome cannot be lost.
CREATE OR REPLACE FUNCTION enqueue_rollout_outcome_webhooks()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    event_name text;
    event_payload jsonb;
BEGIN
    IF NEW.rollout_state IS NOT DISTINCT FROM OLD.rollout_state
       OR OLD.rollout_state NOT IN ('pending', 'rolling_out') THEN
        RETURN NEW;
    END IF;

    IF NEW.rollout_state = 'complete' AND NEW.status = 'live' THEN
        event_name := 'rollout.completed';
        event_payload := jsonb_build_object(
            'app_id', NEW.app_id,
            'deployment_id', NEW.id,
            'rollout_state', 'complete',
            'traffic_percent', NEW.traffic_percent,
            'completed_at', COALESCE(NEW.rollout_completed_at, now())
        );
    ELSIF NEW.rollout_state = 'aborted' AND OLD.status = 'live'
          AND NEW.status IN ('live', 'superseded') THEN
        -- A build that fails before going live is deployment.failed, not a
        -- rollout abort. Include only the customer-visible rollout reason.
        event_name := 'rollout.aborted';
        event_payload := jsonb_build_object(
            'app_id', NEW.app_id,
            'deployment_id', NEW.id,
            'rollout_state', 'aborted',
            'traffic_percent', NEW.traffic_percent,
            'reason', COALESCE(NULLIF(NEW.rollout_aborted_reason, ''), 'rollout aborted'),
            'aborted_at', COALESCE(NEW.rollout_aborted_at, now())
        );
    ELSE
        RETURN NEW;
    END IF;

    INSERT INTO app_webhook_deliveries
        (webhook_id, app_id, account_id, event, payload)
    SELECT h.id, NEW.app_id, h.account_id, event_name, event_payload
      FROM app_webhooks h
      JOIN apps a ON a.id = NEW.app_id AND a.account_id = h.account_id
     WHERE h.app_id = NEW.app_id
       AND h.enabled
       AND (cardinality(h.event_filter) = 0 OR event_name = ANY(h.event_filter));
    RETURN NEW;
END;
$$;

-- Replay-safe if the trigger exists but the goose ledger is behind.
DROP TRIGGER IF EXISTS deployments_rollout_outcome_webhooks ON deployments;
CREATE TRIGGER deployments_rollout_outcome_webhooks
AFTER UPDATE OF rollout_state ON deployments
FOR EACH ROW EXECUTE FUNCTION enqueue_rollout_outcome_webhooks();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS deployments_rollout_outcome_webhooks ON deployments;
DROP FUNCTION IF EXISTS enqueue_rollout_outcome_webhooks();
-- +goose StatementEnd
