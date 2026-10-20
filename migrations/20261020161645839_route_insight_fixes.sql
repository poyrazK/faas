-- migration-compat: expand
-- filename: 20261020161645839_route_insight_fixes.sql

-- ADR-20261020161310: an insight records the fix that resolved it. fixed_at
-- and fix_action are set together, only on a resolved insight, and cleared
-- when the insight reopens. Webhook payloads gain the insight id and the fix.

-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
ALTER TABLE route_insights
    ADD COLUMN IF NOT EXISTS fixed_at timestamptz,
    ADD COLUMN IF NOT EXISTS fix_action text;
-- The checks are added NOT VALID and validated by 20261020161912631, so
-- this transaction never scans route_insights under its lock.
ALTER TABLE route_insights ADD CONSTRAINT route_insights_fix_action_check
    CHECK (fix_action IN ('create_cache_rule', 'set_cache_max_age', 'add_cache_override', 'disable_cache_rule')) NOT VALID;
ALTER TABLE route_insights ADD CONSTRAINT route_insights_fix_pair_check
    CHECK ((fixed_at IS NULL) = (fix_action IS NULL)) NOT VALID;
ALTER TABLE route_insights ADD CONSTRAINT route_insights_fixed_resolved_check
    CHECK (fixed_at IS NULL OR state = 'resolved') NOT VALID;

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
               'id', NEW.id,
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
               'fix_action', NEW.fix_action,
               'fixed_at', NEW.fixed_at,
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
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
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
ALTER TABLE route_insights DROP CONSTRAINT IF EXISTS route_insights_fixed_resolved_check;
ALTER TABLE route_insights DROP CONSTRAINT IF EXISTS route_insights_fix_pair_check;
ALTER TABLE route_insights DROP CONSTRAINT IF EXISTS route_insights_fix_action_check;
ALTER TABLE route_insights DROP COLUMN IF EXISTS fix_action, DROP COLUMN IF EXISTS fixed_at;
-- +goose StatementEnd
