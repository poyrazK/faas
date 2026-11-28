-- Releasing holds and security lifecycle events (ADR-1190).
--
-- An owner may release every hold on a user; the release is a security
-- event ('released'). Two lifecycle events now come from security events,
-- through the same outbox as the others (sql/0003), so they are published
-- only while lifecycle events are on:
--   auth.user.locked      the first refusal by a user's account-wide
--                         password budget or second-step budget in a window
--                         (holds of one address, or messages, raise none),
--                         with the budget and when it reopens;
--   auth.user.new_device  a sign-in from a network and a device the account
--                         had not used in 90 days, with the device label only
--                         (never a network or address, as for every event).
SET LOCAL lock_timeout = '5s';

ALTER TABLE gregale_auth.security_events DROP CONSTRAINT security_events_kind_check,
    ADD CONSTRAINT security_events_kind_check CHECK (kind IN ('password_failed', 'code_failed', 'held', 'captcha_pass', 'trusted_pass', 'message_limited', 'new_device_alert', 'released'));
CREATE INDEX security_events_hold_idx ON gregale_auth.security_events (budget_key) WHERE kind = 'held';

ALTER TABLE gregale_auth.event_outbox DROP CONSTRAINT event_outbox_type_check,
    ADD CONSTRAINT event_outbox_type_check CHECK (type IN ('auth.user.created', 'auth.user.confirmed', 'auth.user.signed_in', 'auth.user.banned', 'auth.user.unbanned', 'auth.user.deleted', 'auth.user.locked', 'auth.user.new_device'));

-- A hold is new when no other row of its budget still holds: rows of one
-- budget differ by network or account, and a release ends them (until).
CREATE FUNCTION gregale_auth.record_security_lifecycle_event() RETURNS trigger
LANGUAGE plpgsql SET search_path = '' AS $$
BEGIN
    IF NEW.user_id IS NULL OR NOT (SELECT enabled FROM gregale_auth.event_settings) THEN
        RETURN NULL;
    END IF;
    IF NEW.kind = 'held' AND NEW.detail->>'budget' IN ('password_account', 'second_step') AND NEW.budget_key IS NOT NULL
        AND NOT EXISTS (SELECT 1 FROM gregale_auth.security_events e
            WHERE e.kind = 'held' AND e.budget_key = NEW.budget_key AND e.id <> NEW.id
              AND (e.detail->>'until')::timestamptz > now()) THEN
        INSERT INTO gregale_auth.event_outbox (type, user_id, data)
        VALUES ('auth.user.locked', NEW.user_id, jsonb_strip_nulls(jsonb_build_object('budget', NEW.detail->>'budget', 'until', NEW.detail->>'until')));
    ELSIF NEW.kind = 'new_device_alert' THEN
        INSERT INTO gregale_auth.event_outbox (type, user_id, data)
        VALUES ('auth.user.new_device', NEW.user_id, jsonb_strip_nulls(jsonb_build_object('device', NEW.detail->>'device')));
    END IF;
    RETURN NULL;
END $$;
REVOKE ALL ON FUNCTION gregale_auth.record_security_lifecycle_event() FROM PUBLIC;

CREATE TRIGGER security_events_lifecycle AFTER INSERT ON gregale_auth.security_events
    FOR EACH ROW EXECUTE FUNCTION gregale_auth.record_security_lifecycle_event();
