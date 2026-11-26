-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';

CREATE TABLE IF NOT EXISTS alert_deferred_fires (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    delivery_id uuid NOT NULL REFERENCES alert_deliveries(id) ON DELETE CASCADE,
    channel_id uuid,
    webhook_url text NOT NULL DEFAULT '',
    channel_only boolean NOT NULL DEFAULT false,
    started boolean NOT NULL DEFAULT false,
    next_attempt_at timestamptz NOT NULL,
    CHECK ((channel_id IS NOT NULL) <> (webhook_url <> ''))
);
CREATE INDEX IF NOT EXISTS alert_deferred_fires_delivery_idx ON alert_deferred_fires (delivery_id);
CREATE INDEX IF NOT EXISTS alert_deferred_fires_due_idx ON alert_deferred_fires (next_attempt_at);

-- A meterd that predates environment scoping uses the same delivery claim
-- insert. Older meters also cannot preserve pending observation handoffs.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_live_alert_delivery_scope() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    rule_scope text;
    observing boolean;
BEGIN
    IF NOT NEW.is_test THEN
        SELECT r.environment_scope, r.observe_only INTO rule_scope, observing
          FROM alert_rules r WHERE r.id = NEW.rule_id FOR UPDATE;
        IF observing OR coalesce(NEW.payload->>'environment_scope', '') <> rule_scope THEN
            RAISE check_violation USING MESSAGE = 'live alert delivery does not match the current rule mode or scope',
                CONSTRAINT = 'alert_deliveries_live_scope';
        END IF;
        IF current_setting('faas.alert_handoff_claim', true) IS DISTINCT FROM 'on' AND (
            EXISTS (SELECT 1 FROM alert_channel_stand_downs s WHERE s.rule_id = NEW.rule_id AND s.observation_transition)
            OR EXISTS (SELECT 1 FROM alert_webhook_stand_downs s WHERE s.rule_id = NEW.rule_id)
        ) THEN
            RAISE check_violation USING MESSAGE = 'live alert delivery requires a handoff-aware evaluator',
                CONSTRAINT = 'alert_deliveries_live_handoff';
        END IF;
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE OR REPLACE TRIGGER alert_deliveries_live_scope
    BEFORE INSERT ON alert_deliveries
    FOR EACH ROW EXECUTE FUNCTION guard_live_alert_delivery_scope();

-- An older meterd can also mistake app-wide recovery for scoped recovery.
-- The new state writers opt in for the duration of their own transaction.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_scoped_alert_rule_state() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.environment_scope <> '' AND NEW.observe_only = OLD.observe_only
       AND NEW.state IS DISTINCT FROM OLD.state
       AND current_setting('faas.scoped_alert_state', true) IS DISTINCT FROM 'on' THEN
        RETURN NULL;
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE OR REPLACE TRIGGER alert_rules_scoped_state
    BEFORE UPDATE OF state ON alert_rules
    FOR EACH ROW EXECUTE FUNCTION guard_scoped_alert_rule_state();

-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT FROM pg_roles WHERE rolname = 'faas_runtime') THEN
        GRANT SELECT, INSERT, UPDATE, DELETE ON alert_deferred_fires TO faas_runtime;
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TRIGGER alert_rules_scoped_state ON alert_rules;
DROP FUNCTION guard_scoped_alert_rule_state();
DROP TRIGGER alert_deliveries_live_scope ON alert_deliveries;
DROP FUNCTION guard_live_alert_delivery_scope();
DROP TABLE alert_deferred_fires;
