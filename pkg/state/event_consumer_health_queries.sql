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
