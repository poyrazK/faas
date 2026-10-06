-- +goose Up
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION workflow_event_recipients(target_account uuid, event_source text, event_type text)
RETURNS TABLE(recipient jsonb) LANGUAGE sql STABLE AS $$
 SELECT jsonb_build_object(
   'id', md5('gregale.workflow.event:' || a.id::text || ':' || (definition->>'name'))::uuid,
   'account_id', a.account_id, 'app_id', a.id, 'deployment_id', d.id,
   'source', definition->'trigger'->>'source', 'type', definition->'trigger'->>'event_type',
   'filter', coalesce(definition->'trigger'->'filter', '{}'::jsonb), 'workflow', definition)
 FROM apps a JOIN accounts ac ON ac.id = a.account_id
 JOIN LATERAL (
   SELECT dep.id, dep.workflows FROM deployments dep
   WHERE dep.app_id = a.id AND dep.status = 'live' AND dep.scope = 'default'
   ORDER BY (dep.traffic_percent > 0) DESC, dep.created_at DESC, dep.id DESC LIMIT 1
 ) d ON true
 CROSS JOIN LATERAL jsonb_array_elements(d.workflows) definition
 WHERE a.account_id = target_account AND a.status <> 'deleted'
   AND NOT a.maintenance_mode AND NOT a.platform_tenant_required
   AND ac.status IN ('active', 'past_due') AND ac.abuse_hold_at IS NULL AND ac.plan <> 'free'
   AND definition->'trigger'->>'type' = 'event'
   AND coalesce(definition->'trigger'->>'enabled', 'true') = 'true'
   AND event_fanout_pattern_matches(definition->'trigger'->>'source', event_source)
   AND event_fanout_pattern_matches(definition->'trigger'->>'event_type', event_type);
$$;
-- +goose StatementEnd

CREATE TABLE IF NOT EXISTS workflow_event_receipts (
 outbox_id bigint NOT NULL REFERENCES event_fanout_outbox(id) ON DELETE CASCADE,
 recipient_id uuid NOT NULL,
 run_id uuid REFERENCES workflow_runs(id) ON DELETE SET NULL,
 admitted_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(outbox_id, recipient_id)
);
CREATE INDEX IF NOT EXISTS workflow_event_receipts_run_idx ON workflow_event_receipts(run_id) WHERE run_id IS NOT NULL;

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
                'fairness_selector', b.fairness_key_selector, 'action', b.action,
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
        'source', s.source, 'type', s.type, 'filter', s.filter,
        'work_snapshot_captured', true,
        'work', CASE WHEN b.subscription_id IS NULL THEN NULL
            ELSE jsonb_build_object(
                'policy_name', b.policy_name, 'key_selector', b.key_selector,
                'fairness_selector', b.fairness_key_selector, 'action', b.action,
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


DROP TABLE workflow_event_receipts;
DROP FUNCTION workflow_event_recipients(uuid, text, text);
