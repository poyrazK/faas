-- +goose Up
-- +goose StatementBegin
-- NULL belongs to receipts accepted before this migration. An empty array
-- means the event had no eligible source/type candidates at acceptance.
ALTER TABLE event_fanout_outbox ADD COLUMN IF NOT EXISTS recipient_snapshot jsonb
    CHECK (recipient_snapshot IS NULL OR jsonb_typeof(recipient_snapshot) = 'array');

CREATE OR REPLACE FUNCTION event_fanout_pattern_matches(pattern text, value text)
RETURNS boolean LANGUAGE sql IMMUTABLE STRICT AS $$
    SELECT CASE
        WHEN pattern = '*' THEN true
        WHEN left(pattern, 1) = '*' AND right(pattern, 1) = '*' THEN
            position(substring(pattern, 2, greatest(length(pattern) - 2, 0)) in value) > 0
        WHEN left(pattern, 1) = '*' THEN
            right(value, greatest(length(pattern) - 1, 0)) = right(pattern, greatest(length(pattern) - 1, 0))
        WHEN right(pattern, 1) = '*' THEN
            left(value, greatest(length(pattern) - 1, 0)) = left(pattern, greatest(length(pattern) - 1, 0))
        ELSE pattern = value
    END
$$;

CREATE OR REPLACE FUNCTION enqueue_event_fanout() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE existing_type text;
DECLARE existing_data jsonb;
DECLARE existing_schema_version text;
DECLARE recipients jsonb;
BEGIN
    IF NEW.kind <> 'event.published' THEN
        RETURN NEW;
    END IF;
    IF NEW.subject IS NULL OR NEW.data->>'source' IS NULL OR
       NEW.data->>'id' IS NULL OR NEW.data->>'type' IS NULL OR
       NEW.data->'data' IS NULL THEN
        RAISE EXCEPTION 'event.published requires account, source, id, type and data'
            USING ERRCODE = '23514';
    END IF;

    SELECT coalesce(jsonb_agg(jsonb_build_object(
        'id', s.id, 'account_id', s.account_id, 'app_id', s.app_id,
        'source', s.source, 'type', s.type, 'filter', s.filter)
        ORDER BY s.created_at, s.id), '[]'::jsonb)
    INTO recipients
    FROM event_subscriptions s
    JOIN apps a ON a.id = s.app_id AND a.account_id = s.account_id
    WHERE s.account_id = NEW.subject AND s.enabled AND a.status <> 'deleted'
      AND event_fanout_pattern_matches(s.source, NEW.data->>'source')
      AND event_fanout_pattern_matches(s.type, NEW.data->>'type');

    INSERT INTO event_fanout_outbox
        (account_id, source, event_id, event_type, schema_version, event_data, payload, recipient_snapshot)
    VALUES (NEW.subject, NEW.data->>'source', NEW.data->>'id',
            NEW.data->>'type', NEW.data->>'schemaversion', NEW.data->'data', NEW.data, recipients)
    ON CONFLICT (account_id, source, event_id) DO NOTHING;
    IF NOT FOUND THEN
        SELECT event_type, schema_version, event_data INTO existing_type, existing_schema_version, existing_data
        FROM event_fanout_outbox WHERE account_id = NEW.subject
          AND source = NEW.data->>'source' AND event_id = NEW.data->>'id';
        IF existing_type IS DISTINCT FROM NEW.data->>'type' OR
           existing_schema_version IS DISTINCT FROM NEW.data->>'schemaversion' OR
           existing_data IS DISTINCT FROM NEW.data->'data' THEN
            RAISE EXCEPTION 'event identity already has different content'
                USING ERRCODE = '23505', CONSTRAINT = 'event_fanout_identity_uniq';
        END IF;
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION enqueue_event_fanout() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE existing_type text;
DECLARE existing_data jsonb;
DECLARE existing_schema_version text;
BEGIN
    IF NEW.kind <> 'event.published' THEN
        RETURN NEW;
    END IF;
    IF NEW.subject IS NULL OR NEW.data->>'source' IS NULL OR
       NEW.data->>'id' IS NULL OR NEW.data->>'type' IS NULL OR
       NEW.data->'data' IS NULL THEN
        RAISE EXCEPTION 'event.published requires account, source, id, type and data'
            USING ERRCODE = '23514';
    END IF;
    INSERT INTO event_fanout_outbox
        (account_id, source, event_id, event_type, schema_version, event_data, payload)
    VALUES (NEW.subject, NEW.data->>'source', NEW.data->>'id',
            NEW.data->>'type', NEW.data->>'schemaversion', NEW.data->'data', NEW.data)
    ON CONFLICT (account_id, source, event_id) DO NOTHING;
    IF NOT FOUND THEN
        SELECT event_type, schema_version, event_data INTO existing_type, existing_schema_version, existing_data
        FROM event_fanout_outbox WHERE account_id = NEW.subject
          AND source = NEW.data->>'source' AND event_id = NEW.data->>'id';
        IF existing_type IS DISTINCT FROM NEW.data->>'type' OR
           existing_schema_version IS DISTINCT FROM NEW.data->>'schemaversion' OR
           existing_data IS DISTINCT FROM NEW.data->'data' THEN
            RAISE EXCEPTION 'event identity already has different content'
                USING ERRCODE = '23505', CONSTRAINT = 'event_fanout_identity_uniq';
        END IF;
    END IF;
    RETURN NEW;
END;
$$;
DROP FUNCTION IF EXISTS event_fanout_pattern_matches(text, text);
ALTER TABLE event_fanout_outbox DROP COLUMN IF EXISTS recipient_snapshot;
-- +goose StatementEnd
