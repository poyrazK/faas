-- Phone sign-in (ADR-1190): a confirmed phone number is reported like a
-- confirmed email address. auth.user.confirmed gains data.channel ('email'
-- or 'phone'); the phone number itself is never recorded, because published
-- events are stored in Gregale's control plane.

CREATE OR REPLACE FUNCTION gregale_auth.record_user_event() RETURNS trigger
LANGUAGE plpgsql SET search_path = '' AS $$
DECLARE
    now_at timestamptz := now();
    old_row jsonb;
    new_row jsonb;
BEGIN
    IF NOT (SELECT enabled FROM gregale_auth.event_settings) THEN
        RETURN NULL;
    END IF;
    IF TG_TABLE_NAME = 'sessions' THEN
        INSERT INTO gregale_auth.event_outbox (type, user_id, data)
        VALUES ('auth.user.signed_in', NEW.user_id, jsonb_strip_nulls(jsonb_build_object('session_id', NEW.id, 'aal', to_jsonb(NEW)->>'aal')));
        RETURN NULL;
    END IF;
    IF TG_OP IN ('INSERT', 'UPDATE') THEN
        new_row := to_jsonb(NEW);
    END IF;
    IF TG_OP = 'UPDATE' THEN
        old_row := to_jsonb(OLD);
    END IF;
    IF TG_OP = 'INSERT' THEN
        INSERT INTO gregale_auth.event_outbox (type, user_id, data)
        VALUES ('auth.user.created', NEW.id, jsonb_build_object(
            'provider', coalesce(new_row->'raw_app_meta_data'->>'provider', 'email'),
            'anonymous', coalesce((new_row->>'is_anonymous')::boolean, false)));
    END IF;
    IF TG_OP IN ('INSERT', 'UPDATE') THEN
        IF new_row->>'email_confirmed_at' IS NOT NULL AND (TG_OP = 'INSERT' OR old_row->>'email_confirmed_at' IS NULL) THEN
            INSERT INTO gregale_auth.event_outbox (type, user_id, data) VALUES ('auth.user.confirmed', NEW.id, '{"channel": "email"}');
        END IF;
        IF new_row->>'phone_confirmed_at' IS NOT NULL AND (TG_OP = 'INSERT' OR old_row->>'phone_confirmed_at' IS NULL) THEN
            INSERT INTO gregale_auth.event_outbox (type, user_id, data) VALUES ('auth.user.confirmed', NEW.id, '{"channel": "phone"}');
        END IF;
    END IF;
    IF TG_OP = 'UPDATE' THEN
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
