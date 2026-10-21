-- migration-compat: expand
-- filename: 20261021223015891_event_order_lane_lookup.sql

-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
-- ADR-795 ordering lanes are (app, work policy, key). The blocker used to read
-- every unsettled receipt of the account, and every recipient in each, to find
-- an earlier delivery in the target's lane, so for an ordered consumer every
-- claim candidate cost O(account backlog). Adopted recipients now carry their
-- lane id (20261021223015842, backfilled in 20261021223015867), so earlier
-- adopted deliveries come from event_fanout_recipients_order_lane_idx; only
-- receipts not yet adopted are still scanned, through
-- event_fanout_order_unadopted_idx. The predicates that decide a blocker are
-- unchanged and applied to every candidate: the lane id only pre-filters.
CREATE OR REPLACE FUNCTION event_recipient_order_blocker(
    target_outbox_id bigint,
    target_subscription_id text,
    target_recipient jsonb,
    include_same_receipt boolean
) RETURNS jsonb LANGUAGE plpgsql STABLE AS $$
DECLARE
    current_account uuid;
    current_payload jsonb;
    target_lane bigint;
BEGIN
    IF coalesce(target_recipient->'work'->>'policy_name', '') = '' THEN
        RETURN NULL;
    END IF;
    SELECT o.account_id, o.payload INTO current_account, current_payload
    FROM event_fanout_outbox o WHERE o.id = target_outbox_id;
    IF NOT FOUND THEN
        RETURN NULL;
    END IF;
    target_lane := event_order_lane_lock_id(target_recipient->>'app_id',
        target_recipient->'work'->>'policy_name', current_payload,
        target_recipient->'work'->>'key_selector');
    IF target_lane IS NULL THEN
        RETURN NULL;
    END IF;
    RETURN (
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
        ), adopted AS (
            -- Adopted receipts: every snapshot recipient has a recipient row,
            -- and an unsettled one in the target's lane carries its lane id.
            SELECT prior.id AS prior_id, prior.source, prior.event_id, prior.created_at,
                prior.available_at, prior.recipient_progress, prior.payload, prior.state AS prior_state,
                item.recipient, item.position, routed.state AS routed_state, routed.available_at AS routed_available_at
            FROM event_fanout_recipients routed
            JOIN event_fanout_outbox prior ON prior.id = routed.outbox_id
            CROSS JOIN LATERAL (
                SELECT i.recipient, i.position
                FROM jsonb_array_elements(coalesce(prior.recipient_snapshot, '[]'::jsonb))
                     WITH ORDINALITY AS i(recipient, position)
                WHERE i.recipient->>'id' = routed.subscription_id
                LIMIT 1
            ) item
            WHERE routed.order_lane = target_lane AND routed.state IN ('pending', 'processing')
              AND prior.recipient_claims AND prior.account_id = current_account
        ), unadopted AS (
            SELECT prior.id AS prior_id, prior.source, prior.event_id, prior.created_at,
                prior.available_at, prior.recipient_progress, prior.payload, prior.state AS prior_state,
                item.recipient, item.position, routed.state AS routed_state, routed.available_at AS routed_available_at
            FROM event_fanout_outbox prior
            CROSS JOIN LATERAL jsonb_array_elements(coalesce(prior.recipient_snapshot, '[]'::jsonb))
                WITH ORDINALITY AS item(recipient, position)
            LEFT JOIN event_fanout_recipients routed
              ON routed.outbox_id = prior.id AND routed.subscription_id = item.recipient->>'id'
            WHERE prior.account_id = current_account AND prior.state <> 'delivered' AND NOT prior.recipient_claims
        ), candidates AS (
            SELECT * FROM adopted UNION ALL SELECT * FROM unadopted
        )
        SELECT (
            SELECT jsonb_build_object(
                'event_source', prior.source, 'event_id', prior.event_id,
                'subscription_id', prior.recipient->>'id', 'accepted_at', prior.created_at,
                'state', coalesce(prior.routed_state, prior.recipient_progress->(prior.recipient->>'id')->>'state', 'pending'),
                'next_attempt_at', CASE WHEN prior.routed_state = 'pending' THEN prior.routed_available_at
                  WHEN prior.routed_state IS NULL AND coalesce(prior.recipient_progress->(prior.recipient->>'id')->>'state','pending') = 'pending'
                  THEN coalesce((prior.recipient_progress->(prior.recipient->>'id')->>'next_attempt_at')::timestamptz, prior.available_at) ELSE NULL END)
            FROM current_event current
            JOIN candidates prior ON true
            WHERE ((prior.prior_id <> current.id AND
                    (coalesce((prior.recipient->'work'->>'routing_order')::bigint, prior.prior_id) < current.routing_order OR
                     (prior.recipient->'work'->>'routing_order' IS NULL AND
                      target_recipient->'work'->>'routing_order' IS NOT NULL AND prior.prior_id < current.id))) OR
                   (include_same_receipt AND prior.prior_id = current.id AND prior.position < current.position))
              AND prior.prior_state <> 'delivered'
              AND prior.recipient->>'app_id' = target_recipient->>'app_id'
              AND prior.recipient->'work'->>'policy_name' = target_recipient->'work'->>'policy_name'
              AND prior.recipient->'work'->>'policy_name' <> ''
              AND event_order_lane_lock_id(prior.recipient->>'app_id',
                  prior.recipient->'work'->>'policy_name', prior.payload,
                  prior.recipient->'work'->>'key_selector') IS NOT NULL
              AND event_order_lane_lock_id(target_recipient->>'app_id',
                  target_recipient->'work'->>'policy_name', current.payload,
                  target_recipient->'work'->>'key_selector') IS NOT NULL
              AND (coalesce((prior.recipient->'work'->>'ordered')::boolean, false) OR
                   prior.recipient->'work'->>'routing_order' IS NOT NULL OR
                   coalesce((target_recipient->'work'->>'ordered')::boolean, false) OR
                   target_recipient->'work'->>'routing_order' IS NOT NULL)
              AND prior.payload #> string_to_array(prior.recipient->'work'->>'key_selector', '.')
                  IS NOT DISTINCT FROM
                  current.payload #> string_to_array(target_recipient->'work'->>'key_selector', '.')
              AND coalesce(prior.routed_state,
                  (prior.recipient_progress->(prior.recipient->>'id'))->>'state', 'pending')
                  NOT IN ('enqueued', 'filtered', 'failed')
            ORDER BY coalesce((prior.recipient->'work'->>'routing_order')::bigint, prior.prior_id), prior.prior_id, prior.position
            LIMIT 1
        )
    );
END;
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
CREATE OR REPLACE FUNCTION event_recipient_order_blocker(
    target_outbox_id bigint,
    target_subscription_id text,
    target_recipient jsonb,
    include_same_receipt boolean
) RETURNS jsonb LANGUAGE plpgsql STABLE AS $$
BEGIN
    IF coalesce(target_recipient->'work'->>'policy_name', '') = '' THEN
        RETURN NULL;
    END IF;
    RETURN (
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
        )
    );
END;
$$;
-- +goose StatementEnd
