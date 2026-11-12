-- filename: 20261112181020456_event_fanout_environment.sql
-- migration-compat: expand
-- ADR-20261104120000: events carry the project environment they belong to
-- (the server-stamped environmentid extension; none is production). Fan-out
-- gives an event only to subscriptions of its own environment, records each
-- recipient's environment for delivery, and keeps workflows, which are
-- production's, to production events. The previous release stamps no
-- environment, so its events route as production events, as before.

-- +goose Up
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION enqueue_event_fanout() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE existing_type text;
DECLARE existing_data jsonb;
DECLARE existing_schema_version text;
DECLARE recipients jsonb;
DECLARE target_app uuid;
DECLARE target_tenant uuid;
DECLARE event_environment uuid;
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

    -- ADR-20261104120000: an event belongs to one project environment of
    -- its account; none is production. Subscriptions receive only their own
    -- environment's events, and only production events reach workflows.
    IF coalesce(NEW.data->>'environmentid', '') <> '' THEN
        BEGIN
            event_environment := (NEW.data->>'environmentid')::uuid;
        EXCEPTION WHEN invalid_text_representation THEN
            RAISE EXCEPTION 'event environment must be a UUID' USING ERRCODE = '23514';
        END;
        IF NOT EXISTS (SELECT 1 FROM project_environments e
                       WHERE e.id = event_environment AND e.account_id = NEW.subject) THEN
            RAISE EXCEPTION 'event environment is not a project environment of its account'
                USING ERRCODE = '23514';
        END IF;
    END IF;

    IF NEW.data ? 'platformtenantid' THEN
        BEGIN
            target_app := (NEW.data->>'appid')::uuid;
            target_tenant := (NEW.data->>'platformtenantid')::uuid;
        EXCEPTION WHEN invalid_text_representation THEN
            RAISE EXCEPTION 'tenant event identity must be UUIDs' USING ERRCODE = '23514';
        END;
        recipients := coalesce((SELECT jsonb_agg(w.recipient ORDER BY w.recipient->>'id')
            FROM workflow_tenant_event_recipients(NEW.subject, target_app, target_tenant,
                NEW.data->>'source', NEW.data->>'type') AS w(recipient)
            WHERE NOT EXISTS (SELECT 1 FROM scenario_test_action_policies x
                WHERE x.app_id = (w.recipient->>'app_id')::uuid)), '[]'::jsonb);
    ELSE
        SELECT coalesce(jsonb_agg(jsonb_build_object(
            'id', s.id, 'account_id', s.account_id, 'app_id', s.app_id,
            'source', s.source, 'type', s.type, 'filter', s.filter,
            'routing_retry_policy', s.routing_retry_policy,
            'acknowledgment_lease_seconds', s.acknowledgment_lease_seconds,
            'schema_versions', s.schema_versions,
            'deployment_scope', s.deployment_scope, 'environment_id', s.environment_id,
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
          AND event_fanout_pattern_matches(s.type, NEW.data->>'type')
          AND s.environment_id IS NOT DISTINCT FROM event_environment
          AND NOT EXISTS (SELECT 1 FROM scenario_test_action_policies x WHERE x.app_id = s.app_id);

        IF event_environment IS NULL THEN
            recipients := recipients || coalesce((SELECT jsonb_agg(w.recipient ORDER BY w.recipient->>'id')
                FROM workflow_event_recipients(NEW.subject, NEW.data->>'source', NEW.data->>'type') AS w(recipient)
                WHERE NOT EXISTS (SELECT 1 FROM scenario_test_action_policies x
                    WHERE x.app_id = (w.recipient->>'app_id')::uuid)), '[]'::jsonb);
        END IF;
        -- ADR-20261025211616: keep the event inside its producer's company.
        recipients := event_recipients_in_producer_reach(NEW.subject, NEW.data->>'producer', recipients);
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
    RETURN NULL;
END;
$$;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION enqueue_event_fanout() RETURNS trigger
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
        recipients := coalesce((SELECT jsonb_agg(w.recipient ORDER BY w.recipient->>'id')
            FROM workflow_tenant_event_recipients(NEW.subject, target_app, target_tenant,
                NEW.data->>'source', NEW.data->>'type') AS w(recipient)
            WHERE NOT EXISTS (SELECT 1 FROM scenario_test_action_policies x
                WHERE x.app_id = (w.recipient->>'app_id')::uuid)), '[]'::jsonb);
    ELSE
        SELECT coalesce(jsonb_agg(jsonb_build_object(
            'id', s.id, 'account_id', s.account_id, 'app_id', s.app_id,
            'source', s.source, 'type', s.type, 'filter', s.filter,
            'routing_retry_policy', s.routing_retry_policy,
            'acknowledgment_lease_seconds', s.acknowledgment_lease_seconds,
            'schema_versions', s.schema_versions,
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
          AND event_fanout_pattern_matches(s.type, NEW.data->>'type')
          AND NOT EXISTS (SELECT 1 FROM scenario_test_action_policies x WHERE x.app_id = s.app_id);

        recipients := recipients || coalesce((SELECT jsonb_agg(w.recipient ORDER BY w.recipient->>'id')
            FROM workflow_event_recipients(NEW.subject, NEW.data->>'source', NEW.data->>'type') AS w(recipient)
            WHERE NOT EXISTS (SELECT 1 FROM scenario_test_action_policies x
                WHERE x.app_id = (w.recipient->>'app_id')::uuid)), '[]'::jsonb);
        -- ADR-20261025211616: keep the event inside its producer's company.
        recipients := event_recipients_in_producer_reach(NEW.subject, NEW.data->>'producer', recipients);
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
    RETURN NULL;
END;
$$;

-- +goose StatementEnd
