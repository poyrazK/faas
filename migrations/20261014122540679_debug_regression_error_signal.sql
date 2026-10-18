-- filename: 20261014122540679_debug_regression_error_signal.sql

-- +goose Up
-- ADR-127 amendment (error-rate regressions): a route observation records
-- which signal regressed. kind is latency (p95, the original detector),
-- errors (the share of 5xx responses rose) or both; one observation per
-- (deployment, route) keeps a single lifecycle for acknowledge, dismiss and
-- resolve. Existing rows are latency observations.
ALTER TABLE debug_regression_observations ADD COLUMN IF NOT EXISTS kind text NOT NULL DEFAULT 'latency';
ALTER TABLE debug_regression_observations ADD COLUMN IF NOT EXISTS error_rate_pct numeric(5,2) NOT NULL DEFAULT 0;
ALTER TABLE debug_regression_observations ADD COLUMN IF NOT EXISTS error_rate_base_pct numeric(5,2) NOT NULL DEFAULT 0;
ALTER TABLE debug_regression_observations ADD COLUMN IF NOT EXISTS error_count integer NOT NULL DEFAULT 0;
-- +goose StatementBegin
DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='debug_regression_observations'::regclass
    AND conname='debug_regression_observations_kind_check') THEN
    ALTER TABLE debug_regression_observations ADD CONSTRAINT debug_regression_observations_kind_check
      CHECK (kind IN ('latency', 'errors', 'latency_and_errors'));
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='debug_regression_observations'::regclass
    AND conname='debug_regression_observations_error_rate_pct_check') THEN
    ALTER TABLE debug_regression_observations ADD CONSTRAINT debug_regression_observations_error_rate_pct_check
      CHECK (error_rate_pct >= 0 AND error_rate_pct <= 100);
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='debug_regression_observations'::regclass
    AND conname='debug_regression_observations_error_rate_base_pct_check') THEN
    ALTER TABLE debug_regression_observations ADD CONSTRAINT debug_regression_observations_error_rate_base_pct_check
      CHECK (error_rate_base_pct >= 0 AND error_rate_base_pct <= 100);
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='debug_regression_observations'::regclass
    AND conname='debug_regression_observations_error_count_check') THEN
    ALTER TABLE debug_regression_observations ADD CONSTRAINT debug_regression_observations_error_count_check
      CHECK (error_count >= 0);
  END IF;
END $$;
-- +goose StatementEnd

-- Same fan-out as 20261009183654862, with the regression kind and error
-- rates added to the payload. Delivery idempotency keys are unchanged. An
-- active latency observation that gains the error signal is reopened by the
-- detector with a new first_detected_at, so it enqueues a fresh detected
-- event under a new transition id.
-- +goose StatementBegin
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
    ELSIF NEW.state = 'active' AND OLD.kind = 'latency' AND NEW.kind <> 'latency' THEN
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
        'kind', NEW.kind,
        'p95_ms', NEW.p95_ms,
        'p95_base_ms', NEW.p95_base_ms,
        'affected_count', NEW.affected_count,
        'regression_factor', NEW.regression_factor,
        'error_rate_pct', NEW.error_rate_pct,
        'error_rate_base_pct', NEW.error_rate_base_pct,
        'error_count', NEW.error_count,
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

-- The state trigger covers reopening an acknowledged, dismissed or resolved
-- observation; this one covers an observation that was already active.
DROP TRIGGER IF EXISTS debug_regression_observations_webhooks_kind ON debug_regression_observations;
CREATE TRIGGER debug_regression_observations_webhooks_kind
AFTER UPDATE OF kind ON debug_regression_observations
FOR EACH ROW
WHEN (OLD.kind = 'latency' AND NEW.kind <> 'latency' AND OLD.state = 'active' AND NEW.state = 'active')
EXECUTE FUNCTION enqueue_debug_regression_webhooks();

-- +goose Down
-- +goose StatementBegin
-- Forward-only: the columns are additive and webhook payloads are durable
-- event history.
SELECT 1;
-- +goose StatementEnd
