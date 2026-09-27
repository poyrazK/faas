-- filename: 20260927182443379_debug_regression_webhooks.sql

-- +goose Up
-- +goose StatementBegin
-- Enqueue customer webhooks in the same transaction as a debugger
-- regression lifecycle transition. The observation table is the source of
-- truth; trigger-based fan-out means an apid crash after the transition
-- cannot lose a notification, and the existing schedd dispatcher owns
-- signing, retries, and dead-letter recovery.

-- One delivery per subscription and lifecycle transition. The transition
-- timestamp is part of the payload so a route that regresses, recovers, and
-- regresses again remains independently deliverable while retries/replayed
-- writes of one transition stay idempotent.
CREATE UNIQUE INDEX IF NOT EXISTS app_webhook_deliveries_debug_regression_transition_uniq
    ON app_webhook_deliveries (
        webhook_id,
        event,
        (payload ->> 'deployment_id'),
        (payload ->> 'route'),
        (payload ->> 'transition_id')
    )
    WHERE event IN ('debug.regression.detected', 'debug.regression.resolved');

CREATE OR REPLACE FUNCTION enqueue_debug_regression_webhooks()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    event_name text;
    event_payload jsonb;
    transition_id timestamptz;
BEGIN
    IF TG_OP = 'INSERT' THEN
        IF NEW.state <> 'active' THEN
            RETURN NEW;
        END IF;
        event_name := 'debug.regression.detected';
        transition_id := NEW.first_detected_at;
    ELSIF NEW.state = 'active' AND OLD.state <> 'active' THEN
        event_name := 'debug.regression.detected';
        transition_id := NEW.first_detected_at;
    ELSIF NEW.state = 'resolved' AND OLD.state <> 'resolved' THEN
        event_name := 'debug.regression.resolved';
        transition_id := NEW.resolved_at;
    ELSE
        RETURN NEW;
    END IF;

    event_payload := jsonb_build_object(
        'app_id', NEW.app_id,
        'deployment_id', NEW.deployment_id,
        'route', NEW.route,
        'p95_ms', NEW.p95_ms,
        'p95_base_ms', NEW.p95_base_ms,
        'affected_count', NEW.affected_count,
        'regression_factor', NEW.regression_factor,
        'first_detected_at', NEW.first_detected_at,
        'last_detected_at', NEW.last_detected_at,
        'state', NEW.state,
        'transition_id', transition_id,
        'resolved_at', NEW.resolved_at
    );

    INSERT INTO app_webhook_deliveries
        (webhook_id, app_id, account_id, event, payload)
    SELECT h.id, NEW.app_id, h.account_id, event_name, event_payload
      FROM app_webhooks h
      JOIN apps a ON a.id = NEW.app_id AND a.account_id = h.account_id
     WHERE h.scope = 'app'
       AND h.app_id = NEW.app_id
       AND h.enabled
       AND (cardinality(h.event_filter) = 0 OR event_name = ANY(h.event_filter))
    ON CONFLICT DO NOTHING;

    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS debug_regression_observations_webhooks_insert
    ON debug_regression_observations;
CREATE TRIGGER debug_regression_observations_webhooks_insert
AFTER INSERT ON debug_regression_observations
FOR EACH ROW EXECUTE FUNCTION enqueue_debug_regression_webhooks();

DROP TRIGGER IF EXISTS debug_regression_observations_webhooks_state
    ON debug_regression_observations;
CREATE TRIGGER debug_regression_observations_webhooks_state
AFTER UPDATE OF state ON debug_regression_observations
FOR EACH ROW
WHEN (OLD.state IS DISTINCT FROM NEW.state)
EXECUTE FUNCTION enqueue_debug_regression_webhooks();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Forward-only: customer webhook deliveries are durable event history and
-- must remain replayable after a downgrade attempt.
SELECT 1;
-- +goose StatementEnd
