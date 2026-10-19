-- migration-compat: expand
-- filename: 20261015093339001_route_insights.sql

-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
-- ADR-1198: proactive route insights. apid's evaluator records one row per
-- app, kind and route; the row is the source of truth and a trigger enqueues
-- the customer webhooks in the same transaction as each transition, like the
-- debugger regression observations. The existing schedd dispatcher owns
-- signing, retries and dead-letter recovery.
CREATE TABLE IF NOT EXISTS route_insights (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    kind text NOT NULL CHECK (kind IN ('kept_awake', 'cacheable', 'cache_stale', 'cache_too_short')),
    method text NOT NULL CHECK (method IN ('GET', 'HEAD', 'POST', 'PUT', 'PATCH', 'DELETE', 'OPTIONS')),
    route text NOT NULL CHECK (route LIKE '/%' AND char_length(route) <= 512),
    state text NOT NULL CHECK (state IN ('active', 'resolved')),
    message text NOT NULL CHECK (char_length(message) BETWEEN 1 AND 1000),
    evidence jsonb NOT NULL CHECK (jsonb_typeof(evidence) = 'object'),
    first_detected_at timestamptz NOT NULL,
    last_detected_at timestamptz NOT NULL,
    resolved_at timestamptz,
    CHECK (last_detected_at >= first_detected_at),
    CHECK ((state = 'resolved') = (resolved_at IS NOT NULL)),
    UNIQUE (app_id, kind, method, route)
);
CREATE INDEX IF NOT EXISTS route_insights_active_idx ON route_insights (app_id) WHERE state = 'active';

-- 20261019072505518 builds the unique index that makes retried transitions
-- idempotent; it builds concurrently, so it lives in its own migration.

CREATE OR REPLACE FUNCTION enqueue_route_insight_webhooks()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    event_name text;
    transition_id timestamptz;
BEGIN
    IF TG_OP = 'INSERT' THEN
        IF NEW.state <> 'active' THEN
            RETURN NEW;
        END IF;
        event_name := 'routes.insight.detected';
        transition_id := NEW.first_detected_at;
    ELSIF NEW.state = 'active' AND OLD.state <> 'active' THEN
        event_name := 'routes.insight.detected';
        transition_id := NEW.first_detected_at;
    ELSIF NEW.state = 'resolved' AND OLD.state <> 'resolved' THEN
        event_name := 'routes.insight.resolved';
        transition_id := NEW.resolved_at;
    ELSE
        RETURN NEW;
    END IF;

    INSERT INTO app_webhook_deliveries (webhook_id, app_id, account_id, event, payload)
    SELECT h.id, NEW.app_id, h.account_id, event_name, jsonb_build_object(
               'app_id', NEW.app_id,
               'kind', NEW.kind,
               'method', NEW.method,
               'route', NEW.route,
               'message', NEW.message,
               'evidence', NEW.evidence,
               'state', NEW.state,
               'first_detected_at', NEW.first_detected_at,
               'last_detected_at', NEW.last_detected_at,
               'resolved_at', NEW.resolved_at,
               'transition_id', transition_id)
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

DROP TRIGGER IF EXISTS route_insights_webhooks_insert ON route_insights;
CREATE TRIGGER route_insights_webhooks_insert
AFTER INSERT ON route_insights
FOR EACH ROW EXECUTE FUNCTION enqueue_route_insight_webhooks();

DROP TRIGGER IF EXISTS route_insights_webhooks_state ON route_insights;
CREATE TRIGGER route_insights_webhooks_state
AFTER UPDATE OF state ON route_insights
FOR EACH ROW
WHEN (OLD.state IS DISTINCT FROM NEW.state)
EXECUTE FUNCTION enqueue_route_insight_webhooks();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS route_insights_webhooks_state ON route_insights;
DROP TRIGGER IF EXISTS route_insights_webhooks_insert ON route_insights;
DROP FUNCTION IF EXISTS enqueue_route_insight_webhooks();
DROP TABLE IF EXISTS route_insights;
-- +goose StatementEnd
