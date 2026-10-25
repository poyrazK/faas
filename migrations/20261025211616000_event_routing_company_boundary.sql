-- migration-compat: expand
-- filename: 20261025211616000_event_routing_company_boundary.sql

-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
-- ADR-20261025211616. A company app's rows stay under its creator's account,
-- so fanout spanned the creator's personal apps and every company they created
-- apps for, whatever their standing in the company now. Producers stamp the
-- event's publisher (app:<id>, org:<id> or personal); an event reaches
-- recipients in its producer's company (or the personal domain) always, and
-- crosses into another one only while the account's owner is an active owner,
-- admin or developer of every company on both sides. Events without a
-- producer (N-1 publishers, platform events) route as before.
CREATE FUNCTION event_app_company(target_app uuid) RETURNS uuid
LANGUAGE sql STABLE AS $$
    SELECT o.id FROM apps a JOIN orgs o ON o.id = a.org_id AND NOT o.personal_org
    WHERE a.id = target_app
$$;

CREATE FUNCTION event_company_reachable(target_account uuid, company uuid) RETURNS boolean
LANGUAGE sql STABLE AS $$
    SELECT EXISTS (SELECT 1 FROM org_memberships m JOIN orgs o ON o.id = m.org_id
        WHERE m.org_id = company AND m.account_id = target_account AND m.removed_at IS NULL
          AND m.role IN ('owner', 'admin', 'developer')
          AND NOT o.deleted_pending AND o.status <> 'deleted_pending')
$$;

CREATE FUNCTION event_recipients_in_producer_reach(target_account uuid, producer text, recipients jsonb) RETURNS jsonb
LANGUAGE plpgsql STABLE AS $$
DECLARE producer_company uuid; producer_reachable boolean;
BEGIN
    IF producer IS NULL OR jsonb_array_length(recipients) = 0 THEN
        RETURN recipients;
    END IF;
    IF producer = 'personal' THEN
        producer_company := NULL;
    ELSIF producer ~* '^org:[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' THEN
        SELECT o.id INTO producer_company FROM orgs o
        WHERE o.id = substr(producer, 5)::uuid AND NOT o.personal_org;
    ELSIF producer ~* '^app:[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' THEN
        producer_company := event_app_company(substr(producer, 5)::uuid);
    ELSE
        RAISE EXCEPTION 'event producer % is not recognized', producer USING ERRCODE = '23514';
    END IF;
    producer_reachable := producer_company IS NULL OR event_company_reachable(target_account, producer_company);
    RETURN coalesce((SELECT jsonb_agg(r.recipient ORDER BY r.position)
        FROM jsonb_array_elements(recipients) WITH ORDINALITY AS r(recipient, position)
        CROSS JOIN LATERAL (SELECT event_app_company((r.recipient->>'app_id')::uuid) AS company) d
        WHERE d.company IS NOT DISTINCT FROM producer_company
           OR (producer_reachable AND (d.company IS NULL OR event_company_reachable(target_account, d.company)))),
        '[]'::jsonb);
END;
$$;

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
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
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
DROP FUNCTION event_recipients_in_producer_reach(uuid, text, jsonb);
DROP FUNCTION event_company_reachable(uuid, uuid);
DROP FUNCTION event_app_company(uuid);
-- +goose StatementEnd
