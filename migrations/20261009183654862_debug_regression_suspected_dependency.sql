-- filename: 20261009183654862_debug_regression_suspected_dependency.sql

-- +goose Up
-- ADR-934 §5: a route regression carries the dependency whose p95 regressed
-- most between the previous and the current deployment, so the alert itself
-- names it. The value is a bounded redacted identity produced by apid
-- (type/kind/name plus both p95 values); it never holds raw span attributes.
ALTER TABLE debug_regression_observations ADD COLUMN IF NOT EXISTS suspected_dependency jsonb;
-- +goose StatementBegin
DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='debug_regression_observations'::regclass
    AND conname='debug_regression_observations_suspected_dependency_check') THEN
    ALTER TABLE debug_regression_observations ADD CONSTRAINT debug_regression_observations_suspected_dependency_check
      CHECK (suspected_dependency IS NULL
             OR (jsonb_typeof(suspected_dependency) = 'object'
                 AND octet_length(suspected_dependency::text) <= 1024));
  END IF;
END $$;

-- Same fan-out as 20260927182443379, with the suspected dependency added to
-- the payload. Delivery idempotency keys are unchanged.
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
        'resolved_at', NEW.resolved_at,
        'suspected_dependency', NEW.suspected_dependency
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
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Forward-only: the column is additive and webhook payloads are durable
-- event history.
SELECT 1;
-- +goose StatementEnd
