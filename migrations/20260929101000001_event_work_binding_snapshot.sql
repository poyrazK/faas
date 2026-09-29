-- +goose Up
-- +goose StatementBegin
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
        'source', s.source, 'type', s.type, 'filter', s.filter,
        'work_snapshot_captured', true,
        'work', CASE WHEN b.subscription_id IS NULL THEN NULL
            ELSE jsonb_build_object(
                'policy_name', b.policy_name, 'key_selector', b.key_selector,
                'fairness_selector', b.fairness_key_selector, 'action', b.action)
            END)
        ORDER BY s.created_at, s.id), '[]'::jsonb)
    INTO recipients
    FROM event_subscriptions s
    JOIN apps a ON a.id = s.app_id AND a.account_id = s.account_id
    LEFT JOIN event_subscription_work_bindings b
      ON b.subscription_id = s.id AND b.app_id = s.app_id
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
