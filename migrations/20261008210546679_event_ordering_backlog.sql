-- +goose Up
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION event_recipient_order_blocker(
    target_outbox_id bigint,
    target_subscription_id text,
    target_recipient jsonb,
    include_same_receipt boolean
) RETURNS jsonb LANGUAGE sql STABLE AS $$
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
SELECT (
    SELECT jsonb_build_object(
        'event_source', prior.source, 'event_id', prior.event_id,
        'subscription_id', item.recipient->>'id', 'accepted_at', prior.created_at,
        'state', coalesce(routed.state, prior.recipient_progress->(item.recipient->>'id')->>'state', 'pending'),
        'next_attempt_at', CASE WHEN routed.state = 'pending' THEN routed.available_at
          WHEN routed.state IS NULL AND coalesce(prior.recipient_progress->(item.recipient->>'id')->>'state','pending') = 'pending'
          THEN coalesce((prior.recipient_progress->(item.recipient->>'id')->>'next_attempt_at')::timestamptz, prior.available_at) ELSE NULL END)
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
    ORDER BY coalesce((item.recipient->'work'->>'routing_order')::bigint, prior.id), prior.id, item.position
    LIMIT 1
);
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION event_recipient_order_blocked(target_outbox_id bigint, target_subscription_id text, target_recipient jsonb, include_same_receipt boolean)
RETURNS boolean LANGUAGE sql STABLE AS $$
SELECT event_recipient_order_blocker(target_outbox_id, target_subscription_id, target_recipient, include_same_receipt) IS NOT NULL;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION event_backlog_waiting_reason(routing_state text, capacity_scope text, routing_mode text, lease_until timestamptz, next_attempt_at timestamptz, consumer_kind text, blocker jsonb, observed_at timestamptz)
RETURNS text LANGUAGE sql IMMUTABLE AS $$
SELECT CASE
 WHEN routing_state = 'processing' THEN 'routing_in_progress'
 WHEN routing_mode = 'event' AND lease_until > observed_at THEN 'receipt_processing'
 WHEN blocker IS NOT NULL THEN 'ordering_blocked'
 WHEN routing_state = 'pending' AND capacity_scope <> '' THEN 'capacity_' || capacity_scope
 WHEN next_attempt_at > observed_at THEN 'retry_backoff'
 WHEN consumer_kind = 'workflow' THEN 'workflow_routing'
 ELSE 'ready' END;
$$;
-- +goose StatementEnd

-- +goose Down
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
DROP FUNCTION event_backlog_waiting_reason(text,text,text,timestamptz,timestamptz,text,jsonb,timestamptz);
DROP FUNCTION event_recipient_order_blocker(bigint,text,jsonb,boolean);
