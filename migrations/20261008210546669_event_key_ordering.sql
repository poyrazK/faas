-- +goose Up
ALTER TABLE event_subscription_work_bindings
    ADD COLUMN ordered boolean NOT NULL DEFAULT false;
CREATE INDEX event_subscription_work_bindings_ordered_lane_idx
    ON event_subscription_work_bindings(app_id, policy_name)
    WHERE ordered;

-- Capture the opt-in bit in the same statement snapshot as the other binding
-- fields; the follow-up projector must not reread mutable subscription state.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION enqueue_event_fanout() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    existing_type text;
    existing_data jsonb;
    existing_schema_version text;
    recipients jsonb;
    target_app uuid;
    target_tenant uuid;
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
-- +goose StatementEnd

-- Transactions publishing the same app/policy/key lane serialize on an
-- advisory lock. This global sequence records that lock order in immutable
-- snapshots without retaining a row for every key ever observed.
CREATE SEQUENCE event_fanout_acceptance_order_seq;
SELECT setval('event_fanout_acceptance_order_seq', greatest(
    coalesce((SELECT last_value FROM event_fanout_outbox_id_seq), 1),
    coalesce(max(id), 0), 1), true)
FROM event_fanout_outbox;

CREATE INDEX event_fanout_order_unsettled_idx
    ON event_fanout_outbox(account_id, id)
    WHERE state <> 'delivered';

-- Keep the SQL lane hash aligned with workpolicy's type-prefixed scalar keys.
-- Numeric scale is removed so 1, 1.0, and 1e0 acquire the same lock.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION event_order_lane_lock_id(
    lane_app_id text,
    lane_policy_name text,
    event_payload jsonb,
    key_selector text
) RETURNS bigint LANGUAGE sql IMMUTABLE STRICT AS $$
WITH selected AS (
    SELECT event_payload #> string_to_array(key_selector, '.') AS value
), canonical AS (
    SELECT CASE jsonb_typeof(value)
        WHEN 'string' THEN 's:' || (value #>> '{}')
        WHEN 'number' THEN 'n:' || trim_scale((value #>> '{}')::numeric)::text
        WHEN 'boolean' THEN 'b:' || (value #>> '{}')
        ELSE NULL
    END AS key
    FROM selected
)
SELECT CASE WHEN key IS NULL OR key = 's:' THEN NULL
    ELSE hashtextextended(jsonb_build_array(lane_app_id, lane_policy_name, key)::text, 0)
    END
FROM canonical;
$$;
-- +goose StatementEnd

-- Only pay the acceptance-order lock cost for a policy lane with ordering
-- enabled. Other bindings on that same lane participate so their invocation
-- or cancellation cannot jump across an ordered subscription.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION event_order_lane_active(
    lane_app_id text,
    lane_policy_name text,
    configured_ordered boolean
) RETURNS boolean LANGUAGE sql STABLE AS $$
SELECT coalesce(configured_ordered, false) OR EXISTS (
    SELECT 1 FROM event_subscription_work_bindings b
    WHERE b.app_id = lane_app_id::uuid
      AND b.policy_name = lane_policy_name
      AND b.ordered
);
$$;
-- +goose StatementEnd

-- Stamp ordering metadata before the publishing transaction becomes visible.
-- Locks are acquired in a stable order to avoid deadlocks when one event
-- contains recipients from several keyed lanes.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION capture_event_recipient_ordering_snapshot() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    snapshot jsonb;
    lane record;
BEGIN
    IF NEW.recipient_snapshot IS NULL OR jsonb_array_length(NEW.recipient_snapshot) = 0 THEN
        RETURN NEW;
    END IF;

    FOR lane IN
        SELECT DISTINCT event_order_lane_lock_id(
            item.recipient->>'app_id', item.recipient->'work'->>'policy_name', NEW.payload,
            item.recipient->'work'->>'key_selector') AS lock_id
        FROM jsonb_array_elements(NEW.recipient_snapshot) item(recipient)
        WHERE jsonb_typeof(item.recipient->'work') = 'object'
          AND item.recipient->'work'->>'policy_name' <> ''
          AND event_order_lane_active(item.recipient->>'app_id',
              item.recipient->'work'->>'policy_name',
              coalesce((item.recipient->'work'->>'ordered')::boolean, false))
          AND event_order_lane_lock_id(
            item.recipient->>'app_id', item.recipient->'work'->>'policy_name', NEW.payload,
            item.recipient->'work'->>'key_selector') IS NOT NULL
        ORDER BY lock_id
    LOOP
        PERFORM pg_advisory_xact_lock(lane.lock_id);
    END LOOP;

    SELECT coalesce(jsonb_agg(
        CASE WHEN jsonb_typeof(item.recipient->'work') = 'object'
                   AND event_order_lane_active(item.recipient->>'app_id',
                       item.recipient->'work'->>'policy_name',
                       coalesce((item.recipient->'work'->>'ordered')::boolean, false))
                   AND event_order_lane_lock_id(item.recipient->>'app_id',
                       item.recipient->'work'->>'policy_name', NEW.payload,
                       item.recipient->'work'->>'key_selector') IS NOT NULL
             THEN jsonb_set(item.recipient, '{work,routing_order}',
                       to_jsonb(nextval('event_fanout_acceptance_order_seq')::bigint), true)
             ELSE item.recipient
        END ORDER BY item.position), '[]'::jsonb)
    INTO snapshot
    FROM jsonb_array_elements(NEW.recipient_snapshot) WITH ORDINALITY AS item(recipient, position);

    UPDATE event_fanout_outbox SET recipient_snapshot = snapshot WHERE id = NEW.id;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER event_fanout_ordering_snapshot
AFTER INSERT ON event_fanout_outbox
FOR EACH ROW EXECUTE FUNCTION capture_event_recipient_ordering_snapshot();

-- A recipient waits until earlier same-lane routing is terminal. The
-- invocation work lane then serializes dispatch and automatic execution
-- retries for that key.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION event_recipient_order_blocked(
    target_outbox_id bigint,
    target_subscription_id text,
    target_recipient jsonb,
    include_same_receipt boolean
) RETURNS boolean LANGUAGE sql STABLE AS $$
WITH current_event AS (
    SELECT o.id, o.account_id, o.payload,
        (SELECT item.position
         FROM jsonb_array_elements(coalesce(o.recipient_snapshot, '[]'::jsonb))
              WITH ORDINALITY AS item(recipient, position)
         WHERE item.recipient->>'id' = target_subscription_id
         LIMIT 1) AS position,
        (SELECT coalesce((item.recipient->'work'->>'routing_order')::bigint, o.id)
         FROM jsonb_array_elements(coalesce(o.recipient_snapshot, '[]'::jsonb))
              WITH ORDINALITY AS item(recipient, position)
         WHERE item.recipient->>'id' = target_subscription_id
         LIMIT 1) AS routing_order
    FROM event_fanout_outbox o
    WHERE o.id = target_outbox_id
)
SELECT EXISTS (
    SELECT 1
    FROM current_event current
    JOIN event_fanout_outbox prior ON prior.account_id = current.account_id
    CROSS JOIN LATERAL jsonb_array_elements(coalesce(prior.recipient_snapshot, '[]'::jsonb))
        WITH ORDINALITY AS item(recipient, position)
    LEFT JOIN event_fanout_recipients routed
      ON routed.outbox_id = prior.id AND routed.subscription_id = item.recipient->>'id'
    WHERE ((prior.id <> current.id AND
            (coalesce((item.recipient->'work'->>'routing_order')::bigint, prior.id) < current.routing_order OR
             (item.recipient->'work'->>'routing_order' IS NULL AND
              target_recipient->'work'->>'routing_order' IS NOT NULL AND prior.id < current.id))) OR
           (include_same_receipt AND prior.id = current.id AND item.position < current.position))
      AND prior.state <> 'delivered'
      AND item.recipient->>'app_id' = target_recipient->>'app_id'
      AND item.recipient->'work'->>'policy_name' = target_recipient->'work'->>'policy_name'
      AND item.recipient->'work'->>'policy_name' <> ''
      AND event_order_lane_lock_id(item.recipient->>'app_id',
          item.recipient->'work'->>'policy_name', prior.payload,
          item.recipient->'work'->>'key_selector') IS NOT NULL
      AND event_order_lane_lock_id(target_recipient->>'app_id',
          target_recipient->'work'->>'policy_name', current.payload,
          target_recipient->'work'->>'key_selector') IS NOT NULL
      AND (coalesce((item.recipient->'work'->>'ordered')::boolean, false) OR
           item.recipient->'work'->>'routing_order' IS NOT NULL OR
           coalesce((target_recipient->'work'->>'ordered')::boolean, false) OR
           target_recipient->'work'->>'routing_order' IS NOT NULL)
      AND prior.payload #> string_to_array(item.recipient->'work'->>'key_selector', '.')
          IS NOT DISTINCT FROM
          current.payload #> string_to_array(target_recipient->'work'->>'key_selector', '.')
      AND coalesce(routed.state,
          (prior.recipient_progress->(item.recipient->>'id'))->>'state', 'pending')
          NOT IN ('enqueued', 'filtered', 'failed')
);
$$;
-- +goose StatementEnd

-- +goose Down
DROP FUNCTION event_recipient_order_blocked(bigint, text, jsonb, boolean);
DROP TRIGGER event_fanout_ordering_snapshot ON event_fanout_outbox;
DROP FUNCTION capture_event_recipient_ordering_snapshot();
DROP FUNCTION event_order_lane_active(text, text, boolean);
DROP FUNCTION event_order_lane_lock_id(text, text, jsonb, text);
DROP SEQUENCE event_fanout_acceptance_order_seq;
DROP INDEX event_fanout_order_unsettled_idx;
DROP INDEX event_subscription_work_bindings_ordered_lane_idx;
-- Restore the pre-ordering event snapshot projector before removing its column.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION enqueue_event_fanout() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    existing_type text;
    existing_data jsonb;
    existing_schema_version text;
    recipients jsonb;
    target_app uuid;
    target_tenant uuid;
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
ALTER TABLE event_subscription_work_bindings DROP COLUMN ordered;
