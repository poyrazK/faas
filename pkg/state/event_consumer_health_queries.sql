-- name: EventConsumerHealthHistory :one
SELECT count(*) FILTER (WHERE h.state='enqueued')::bigint AS successful_routes,
 count(*) FILTER (WHERE h.state='failed')::bigint AS terminal_failures,
 count(*) FILTER (WHERE h.state='failed' AND h.failure_code='delivery_expired')::bigint AS expired_deliveries,
 count(*) FILTER (WHERE h.state='pending' AND h.last_error<>'' AND h.capacity_scope='')::bigint AS retry_scheduled,
 coalesce(percentile_cont(0.95) WITHIN GROUP (ORDER BY greatest(0,extract(epoch FROM h.occurred_at-o.created_at))) FILTER (WHERE h.state='enqueued'),0)::double precision AS latency_p95_seconds
FROM event_fanout_attempt_history h JOIN event_fanout_outbox o ON o.id=h.outbox_id
WHERE o.account_id=sqlc.arg(account_id)::uuid AND h.app_id=sqlc.arg(app_id)::uuid AND h.subscription_id=sqlc.arg(subscription_id)::text
 AND h.occurred_at>=sqlc.arg(since_at)::timestamptz AND h.occurred_at<=sqlc.arg(now_at)::timestamptz
 AND h.action IN ('fanout_attempt','backfill_attempt');

-- name: EventConsumerHealthCompacted :one
SELECT EXISTS (SELECT 1 FROM event_fanout_history_summaries h JOIN event_fanout_outbox o ON o.id=h.outbox_id
 WHERE o.account_id=sqlc.arg(account_id)::uuid AND h.app_id=sqlc.arg(app_id)::uuid AND h.subscription_id=sqlc.arg(subscription_id)::text
 AND h.compacted_through_at>=sqlc.arg(since_at)::timestamptz) AS compacted;

-- name: EventConsumerPressureTarget :one
SELECT a.id FROM apps a WHERE a.id=sqlc.arg(app_id)::uuid AND a.account_id=sqlc.arg(account_id)::uuid AND a.status<>'deleted'
 AND (EXISTS (SELECT 1 FROM event_subscriptions s WHERE s.id=sqlc.arg(subscription_id)::uuid AND s.app_id=a.id AND s.account_id=a.account_id)
 OR EXISTS (SELECT 1 FROM event_subscription_delivery_controls c WHERE c.subscription_id=sqlc.arg(subscription_id)::uuid AND c.app_id=a.id AND c.account_id=a.account_id));

-- name: EventConsumerPressureWaits :one
SELECT count(*) FILTER (WHERE observation.reason='capacity_consumer')::bigint AS consumer_waiting,
 count(*) FILTER (WHERE observation.reason='capacity_app')::bigint AS app_waiting,
 count(*) FILTER (WHERE observation.reason='capacity_account')::bigint AS account_waiting,
 min(b.accepted_at) FILTER (WHERE observation.reason='capacity_consumer')::timestamptz AS consumer_oldest,
 min(b.accepted_at) FILTER (WHERE observation.reason='capacity_app')::timestamptz AS app_oldest,
 min(b.accepted_at) FILTER (WHERE observation.reason='capacity_account')::timestamptz AS account_oldest
FROM event_routing_backlog b JOIN event_fanout_outbox o ON o.id=b.outbox_id AND o.account_id=b.account_id LEFT JOIN apps a ON a.id=b.app_id AND a.account_id=b.account_id

LEFT JOIN LATERAL (SELECT event_recipient_order_blocker(b.outbox_id,b.subscription_id,
 (SELECT item FROM jsonb_array_elements(coalesce(o.recipient_snapshot,'[]'::jsonb)) item WHERE item->>'id'=b.subscription_id LIMIT 1),b.routing_mode='recipient') AS blocker) ordering ON b.routing_state='pending'
CROSS JOIN LATERAL (SELECT event_subscription_delivery_waiting_reason(b.account_id,b.app_id,b.subscription_id,sqlc.arg(observed_at)::timestamptz) AS reason) control
CROSS JOIN LATERAL (SELECT CASE WHEN b.routing_state='pending' AND NOT (b.routing_mode='event' AND coalesce(b.lease_until>sqlc.arg(observed_at)::timestamptz,false)) AND control.reason<>'' THEN control.reason ELSE event_backlog_waiting_reason(b.routing_state,b.capacity_scope,b.routing_mode,b.lease_until,b.next_attempt_at,b.consumer_kind,ordering.blocker,sqlc.arg(observed_at)::timestamptz) END AS reason) observation
WHERE b.account_id=sqlc.arg(account_id)::uuid AND b.app_id=sqlc.arg(app_id)::uuid
 AND b.subscription_id=sqlc.arg(subscription_id)::text AND b.consumer_kind='application'
 AND b.routing_state='pending' AND b.accepted_at<=sqlc.arg(observed_at)::timestamptz;
