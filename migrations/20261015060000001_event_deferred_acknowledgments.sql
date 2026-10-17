-- +goose Up
ALTER TABLE event_subscriptions ADD COLUMN IF NOT EXISTS acknowledgment_lease_seconds integer NOT NULL DEFAULT 0 CHECK (acknowledgment_lease_seconds BETWEEN 0 AND 300);
CREATE TABLE IF NOT EXISTS event_delivery_acknowledgments (
 invocation_id uuid PRIMARY KEY REFERENCES invocations(id) ON DELETE CASCADE,
 attempt integer NOT NULL CHECK (attempt > 0),
 replay_generation bigint NOT NULL CHECK (replay_generation >= 0),
 token_digest bytea NOT NULL CHECK (octet_length(token_digest) = 32),
 phase text NOT NULL CHECK (phase IN ('offered','awaiting','completed')),
 completion_digest bytea CHECK (completion_digest IS NULL OR octet_length(completion_digest) = 32),
 CHECK ((phase = 'completed') = (completion_digest IS NOT NULL)),
 lease_seconds integer NOT NULL CHECK (lease_seconds BETWEEN 1 AND 300),
 deadline_at timestamptz NOT NULL,
 max_attempts integer NOT NULL CHECK (max_attempts > 0),
 retry_delay_ms bigint NOT NULL CHECK (retry_delay_ms > 0)
);

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
        recipients := coalesce((SELECT jsonb_agg(recipient ORDER BY recipient->>'id')
            FROM workflow_tenant_event_recipients(NEW.subject, target_app, target_tenant,
                NEW.data->>'source', NEW.data->>'type')), '[]'::jsonb);
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
SELECT 1; -- Retain durable acknowledgments and acceptance snapshots on rollback.
