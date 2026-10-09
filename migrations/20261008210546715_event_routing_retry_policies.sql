-- +goose Up
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION valid_event_routing_retry_policy(p jsonb) RETURNS boolean LANGUAGE sql IMMUTABLE AS $$
 SELECT CASE WHEN p IS NULL THEN true
 WHEN jsonb_typeof(p)<>'object' OR NOT (p ?& ARRAY['max_attempts','max_retry_duration_ms','initial_backoff_ms','max_backoff_ms','jitter']) THEN false
 WHEN jsonb_typeof(p->'max_attempts')<>'number' OR jsonb_typeof(p->'max_retry_duration_ms')<>'number' OR jsonb_typeof(p->'initial_backoff_ms')<>'number' OR jsonb_typeof(p->'max_backoff_ms')<>'number' OR jsonb_typeof(p->'jitter')<>'boolean' THEN false
 ELSE (p->>'max_attempts')::numeric BETWEEN 1 AND 100 AND (p->>'max_attempts')::numeric % 1=0
 AND (p->>'max_retry_duration_ms')::numeric BETWEEN 0 AND 604800000 AND (p->>'max_retry_duration_ms')::numeric % 1=0
 AND (p->>'initial_backoff_ms')::numeric BETWEEN 1 AND 3600000 AND (p->>'initial_backoff_ms')::numeric % 1=0
 AND (p->>'max_backoff_ms')::numeric BETWEEN (p->>'initial_backoff_ms')::numeric AND 3600000 AND (p->>'max_backoff_ms')::numeric % 1=0 END;
$$;
ALTER TABLE event_subscriptions
    ADD COLUMN IF NOT EXISTS routing_retry_policy jsonb CHECK (valid_event_routing_retry_policy(routing_retry_policy));
ALTER TABLE event_fanout_attempt_history
    ADD COLUMN IF NOT EXISTS retry_stop_reason text NOT NULL DEFAULT '' CHECK (retry_stop_reason IN ('','non_retryable','max_attempts','max_duration'));
ALTER TABLE event_fanout_attempt_history
    DROP COLUMN IF EXISTS history_bytes;
ALTER TABLE event_fanout_attempt_history
    ADD COLUMN IF NOT EXISTS history_bytes bigint GENERATED ALWAYS AS (((((((((128)::bigint + octet_length(subscription_id)) + octet_length(action)) + octet_length(state)) + octet_length(failure_code)) + octet_length(last_error)) + octet_length(capacity_scope))) + octet_length(retry_stop_reason)) STORED NOT NULL CHECK (history_bytes >= 0);
CREATE OR REPLACE FUNCTION public.enqueue_event_fanout() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE existing_type text;
DECLARE existing_data jsonb;
DECLARE existing_schema_version text;
DECLARE recipients jsonb;
DECLARE target_app uuid;
DECLARE target_tenant uuid;
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

    IF NEW.data ? 'platformtenantid' THEN
        BEGIN
            target_app := (NEW.data->>'appid')::uuid;
            target_tenant := (NEW.data->>'platformtenantid')::uuid;
        EXCEPTION WHEN invalid_text_representation THEN
            RAISE EXCEPTION 'tenant event identity must be UUIDs' USING ERRCODE = '23514';
        END;
        recipients := coalesce((SELECT jsonb_agg(recipient ORDER BY recipient->>'id')
            FROM workflow_tenant_event_recipients(NEW.subject, target_app, target_tenant,
                NEW.data->>'source', NEW.data->>'type')), '[]'::jsonb);
    ELSE
        SELECT coalesce(jsonb_agg(jsonb_build_object(
            'id', s.id, 'account_id', s.account_id, 'app_id', s.app_id,
            'source', s.source, 'type', s.type, 'filter', s.filter,
            'routing_retry_policy', s.routing_retry_policy,
            'work_snapshot_captured', true,
            'work', CASE WHEN b.subscription_id IS NULL THEN NULL
                ELSE jsonb_build_object(
                    'policy_name', b.policy_name, 'key_selector', b.key_selector,
                    'fairness_selector', b.fairness_key_selector, 'action', b.action,
                    'ordered', b.ordered,
                    'policy', CASE WHEN p.name IS NULL THEN NULL
                        ELSE jsonb_build_object(
                            'revision', p.revision,
                            'max_running_per_key', p.max_running_per_key,
                            'max_running_per_fairness_key', p.max_running_per_fairness_key,
                            'pending_updates', p.pending_updates,
                            'debounce_ms', p.debounce_ms,
                            'expires_after_ms', p.expires_after_ms) END)
                END)
            ORDER BY s.created_at, s.id), '[]'::jsonb)
        INTO recipients
        FROM event_subscriptions s
        JOIN apps a ON a.id = s.app_id AND a.account_id = s.account_id
        LEFT JOIN event_subscription_work_bindings b
          ON b.subscription_id = s.id AND b.app_id = s.app_id
        LEFT JOIN app_work_policies p
          ON p.app_id = b.app_id AND p.name = b.policy_name
        WHERE s.account_id = NEW.subject AND s.enabled AND a.status <> 'deleted'
          AND event_fanout_pattern_matches(s.source, NEW.data->>'source')
          AND event_fanout_pattern_matches(s.type, NEW.data->>'type');

        recipients := recipients || coalesce((SELECT jsonb_agg(recipient ORDER BY recipient->>'id')
            FROM workflow_event_recipients(NEW.subject, NEW.data->>'source', NEW.data->>'type')), '[]'::jsonb);
    END IF;

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
CREATE OR REPLACE FUNCTION public.enqueue_event_fanout() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE existing_type text;
DECLARE existing_data jsonb;
DECLARE existing_schema_version text;
DECLARE recipients jsonb;
DECLARE target_app uuid;
DECLARE target_tenant uuid;
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

    IF NEW.data ? 'platformtenantid' THEN
        BEGIN
            target_app := (NEW.data->>'appid')::uuid;
            target_tenant := (NEW.data->>'platformtenantid')::uuid;
        EXCEPTION WHEN invalid_text_representation THEN
            RAISE EXCEPTION 'tenant event identity must be UUIDs' USING ERRCODE = '23514';
        END;
        recipients := coalesce((SELECT jsonb_agg(recipient ORDER BY recipient->>'id')
            FROM workflow_tenant_event_recipients(NEW.subject, target_app, target_tenant,
                NEW.data->>'source', NEW.data->>'type')), '[]'::jsonb);
    ELSE
        SELECT coalesce(jsonb_agg(jsonb_build_object(
            'id', s.id, 'account_id', s.account_id, 'app_id', s.app_id,
            'source', s.source, 'type', s.type, 'filter', s.filter,
            'work_snapshot_captured', true,
            'work', CASE WHEN b.subscription_id IS NULL THEN NULL
                ELSE jsonb_build_object(
                    'policy_name', b.policy_name, 'key_selector', b.key_selector,
                    'fairness_selector', b.fairness_key_selector, 'action', b.action,
                    'ordered', b.ordered,
                    'policy', CASE WHEN p.name IS NULL THEN NULL
                        ELSE jsonb_build_object(
                            'revision', p.revision,
                            'max_running_per_key', p.max_running_per_key,
                            'max_running_per_fairness_key', p.max_running_per_fairness_key,
                            'pending_updates', p.pending_updates,
                            'debounce_ms', p.debounce_ms,
                            'expires_after_ms', p.expires_after_ms) END)
                END)
            ORDER BY s.created_at, s.id), '[]'::jsonb)
        INTO recipients
        FROM event_subscriptions s
        JOIN apps a ON a.id = s.app_id AND a.account_id = s.account_id
        LEFT JOIN event_subscription_work_bindings b
          ON b.subscription_id = s.id AND b.app_id = s.app_id
        LEFT JOIN app_work_policies p
          ON p.app_id = b.app_id AND p.name = b.policy_name
        WHERE s.account_id = NEW.subject AND s.enabled AND a.status <> 'deleted'
          AND event_fanout_pattern_matches(s.source, NEW.data->>'source')
          AND event_fanout_pattern_matches(s.type, NEW.data->>'type');

        recipients := recipients || coalesce((SELECT jsonb_agg(recipient ORDER BY recipient->>'id')
            FROM workflow_event_recipients(NEW.subject, NEW.data->>'source', NEW.data->>'type')), '[]'::jsonb);
    END IF;

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
ALTER TABLE event_fanout_attempt_history DROP COLUMN history_bytes;
ALTER TABLE event_fanout_attempt_history DROP COLUMN retry_stop_reason;
ALTER TABLE event_fanout_attempt_history ADD COLUMN history_bytes bigint GENERATED ALWAYS AS ((((((((128)::bigint + octet_length(subscription_id)) + octet_length(action)) + octet_length(state)) + octet_length(failure_code)) + octet_length(last_error)) + octet_length(capacity_scope))) STORED NOT NULL CHECK (history_bytes >= 0);
ALTER TABLE event_subscriptions DROP COLUMN routing_retry_policy;
DROP FUNCTION valid_event_routing_retry_policy(jsonb);
-- +goose StatementEnd
