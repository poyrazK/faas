-- Lifecycle events and the sign-in activity view (ADR-1190).
--
-- Triggers on GoTrue's tables record user changes in an outbox that the
-- front publishes to Gregale's event bus as app-scoped events. A row holds
-- the user ID and non-personal facts only: never an address, metadata or a
-- token, because published events are stored in Gregale's control plane.
-- Recording is off until the front enables it at startup, so an app without
-- lifecycle events accumulates nothing.

CREATE TABLE gregale_auth.event_settings (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    enabled boolean NOT NULL DEFAULT false
);
INSERT INTO gregale_auth.event_settings DEFAULT VALUES;

CREATE TABLE gregale_auth.event_outbox (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    type text NOT NULL CHECK (type IN ('auth.user.created', 'auth.user.confirmed', 'auth.user.signed_in', 'auth.user.banned', 'auth.user.unbanned', 'auth.user.deleted')),
    user_id uuid NOT NULL,
    data jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(data) = 'object'),
    occurred_at timestamptz NOT NULL DEFAULT now(),
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    next_attempt_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX event_outbox_due_idx ON gregale_auth.event_outbox (next_attempt_at, occurred_at);

-- Optional GoTrue columns are read through to_jsonb so the trigger does not
-- depend on a column that a GoTrue release adds or renames.
CREATE FUNCTION gregale_auth.record_user_event() RETURNS trigger
LANGUAGE plpgsql SET search_path = '' AS $$
DECLARE
    now_at timestamptz := now();
BEGIN
    IF NOT (SELECT enabled FROM gregale_auth.event_settings) THEN
        RETURN NULL;
    END IF;
    IF TG_TABLE_NAME = 'sessions' THEN
        INSERT INTO gregale_auth.event_outbox (type, user_id, data)
        VALUES ('auth.user.signed_in', NEW.user_id, jsonb_strip_nulls(jsonb_build_object('session_id', NEW.id, 'aal', to_jsonb(NEW)->>'aal')));
    ELSIF TG_OP = 'INSERT' THEN
        INSERT INTO gregale_auth.event_outbox (type, user_id, data)
        VALUES ('auth.user.created', NEW.id, jsonb_build_object(
            'provider', coalesce(to_jsonb(NEW)->'raw_app_meta_data'->>'provider', 'email'),
            'anonymous', coalesce((to_jsonb(NEW)->>'is_anonymous')::boolean, false)));
        IF NEW.email_confirmed_at IS NOT NULL THEN
            INSERT INTO gregale_auth.event_outbox (type, user_id) VALUES ('auth.user.confirmed', NEW.id);
        END IF;
    ELSIF TG_OP = 'UPDATE' THEN
        IF OLD.email_confirmed_at IS NULL AND NEW.email_confirmed_at IS NOT NULL THEN
            INSERT INTO gregale_auth.event_outbox (type, user_id) VALUES ('auth.user.confirmed', NEW.id);
        END IF;
        IF NEW.banned_until > now_at AND (OLD.banned_until IS NULL OR OLD.banned_until <= now_at) THEN
            INSERT INTO gregale_auth.event_outbox (type, user_id, data) VALUES ('auth.user.banned', NEW.id, jsonb_build_object('until', NEW.banned_until));
        ELSIF OLD.banned_until > now_at AND (NEW.banned_until IS NULL OR NEW.banned_until <= now_at) THEN
            INSERT INTO gregale_auth.event_outbox (type, user_id) VALUES ('auth.user.unbanned', NEW.id);
        END IF;
        IF OLD.deleted_at IS NULL AND NEW.deleted_at IS NOT NULL THEN
            INSERT INTO gregale_auth.event_outbox (type, user_id) VALUES ('auth.user.deleted', NEW.id);
        END IF;
    ELSIF TG_OP = 'DELETE' AND OLD.deleted_at IS NULL THEN
        INSERT INTO gregale_auth.event_outbox (type, user_id) VALUES ('auth.user.deleted', OLD.id);
    END IF;
    RETURN NULL;
END
$$;

CREATE TRIGGER gregale_auth_user_events AFTER INSERT OR UPDATE OR DELETE ON auth.users
    FOR EACH ROW EXECUTE FUNCTION gregale_auth.record_user_event();
CREATE TRIGGER gregale_auth_session_events AFTER INSERT ON auth.sessions
    FOR EACH ROW EXECUTE FUNCTION gregale_auth.record_user_event();

-- The owner's activity view reads GoTrue's audit log newest first, overall
-- or for one user, who is either the actor or, for an administrator's
-- action, the affected user. GoTrue indexes the log only by instance.
CREATE INDEX IF NOT EXISTS gregale_auth_audit_created_idx ON auth.audit_log_entries (created_at DESC);
CREATE INDEX IF NOT EXISTS gregale_auth_audit_actor_idx ON auth.audit_log_entries ((payload->>'actor_id'), created_at DESC);
CREATE INDEX IF NOT EXISTS gregale_auth_audit_subject_idx ON auth.audit_log_entries ((payload->'traits'->>'user_id'), created_at DESC);
