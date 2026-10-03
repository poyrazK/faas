-- filename: 20260924080306504_account_release_webhook_fanout.sql

-- +goose Up
-- +goose StatementBegin
-- ADR-224: release transitions enqueue for both app and account receivers.
-- Keep the source app/account join in the same transaction as the transition:
-- a receiver from another account must never see the app or its payload.
-- Existing triggers call these functions, so replacing them is sufficient.
CREATE OR REPLACE FUNCTION enqueue_deployment_lifecycle_webhooks()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    event_name text;
    event_payload jsonb;
BEGIN
    IF NEW.status = OLD.status OR NEW.status NOT IN ('live', 'failed') THEN
        RETURN NEW;
    END IF;

    event_name := 'deployment.' || NEW.status;
    event_payload := jsonb_build_object(
        'app_id', NEW.app_id,
        'deployment_id', NEW.id,
        'status', NEW.status
    );
    IF NEW.status = 'failed' THEN
        event_payload := event_payload || jsonb_strip_nulls(jsonb_build_object(
            'error_code', NEW.error_code,
            'error_hint', NEW.error_hint,
            'error_why', NEW.error_why,
            'error_fix', NEW.error_fix
        ));
    END IF;

    INSERT INTO app_webhook_deliveries
        (webhook_id, app_id, account_id, event, payload)
    SELECT h.id, NEW.app_id, h.account_id, event_name, event_payload
      FROM app_webhooks h
      JOIN apps a ON a.id = NEW.app_id AND a.account_id = h.account_id
     WHERE h.enabled
       AND ((h.scope = 'app' AND h.app_id = NEW.app_id)
            OR (h.scope = 'account' AND h.app_id IS NULL))
       AND (event_name = ANY(h.event_filter)
            OR (h.scope = 'app' AND cardinality(h.event_filter) = 0));
    RETURN NEW;
END;
$$;

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
     WHERE h.enabled
       AND ((h.scope = 'app' AND h.app_id = NEW.app_id)
            OR (h.scope = 'account' AND h.app_id IS NULL))
       AND (event_name = ANY(h.event_filter)
            OR (h.scope = 'app' AND cardinality(h.event_filter) = 0));
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Forward-only: after account receivers are configured, reverting this
-- fan-out would silently drop their release notifications.
SELECT 1;
-- +goose StatementEnd
