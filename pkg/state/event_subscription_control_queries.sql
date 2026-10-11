-- name: EventSubscriptionControlLock :exec
SELECT pg_advisory_xact_lock(hashtextextended(sqlc.arg(subscription_id)::text,719));

-- name: EventSubscriptionControlTarget :one
SELECT a.id FROM apps a WHERE a.id=sqlc.arg(app_id)::uuid AND a.account_id=sqlc.arg(account_id)::uuid AND a.status<>'deleted'
 AND (EXISTS (SELECT 1 FROM event_subscriptions s WHERE s.id=sqlc.arg(subscription_id)::uuid AND s.app_id=a.id AND s.account_id=a.account_id)
 OR EXISTS (SELECT 1 FROM event_subscription_delivery_controls c WHERE c.subscription_id=sqlc.arg(subscription_id)::uuid AND c.app_id=a.id AND c.account_id=a.account_id)) FOR SHARE;

-- name: EventSubscriptionControlGet :one
SELECT * FROM event_subscription_delivery_controls WHERE subscription_id=sqlc.arg(subscription_id)::uuid AND app_id=sqlc.arg(app_id)::uuid AND account_id=sqlc.arg(account_id)::uuid;

-- name: EventSubscriptionControlSet :exec
INSERT INTO event_subscription_delivery_controls(subscription_id,account_id,app_id,paused,rate_per_second,window_started_at,updated_at,paused_at)
VALUES (sqlc.arg(subscription_id)::uuid,sqlc.arg(account_id)::uuid,sqlc.arg(app_id)::uuid,sqlc.arg(paused)::boolean,sqlc.arg(rate_per_second)::integer,sqlc.arg(now_at)::timestamptz,sqlc.arg(now_at)::timestamptz,CASE WHEN sqlc.arg(paused)::boolean THEN sqlc.arg(now_at)::timestamptz END)
ON CONFLICT(subscription_id) DO UPDATE SET paused=excluded.paused,
 rate_per_second=CASE WHEN excluded.paused THEN event_subscription_delivery_controls.rate_per_second ELSE excluded.rate_per_second END,
 window_started_at=CASE WHEN NOT excluded.paused AND (event_subscription_delivery_controls.paused OR event_subscription_delivery_controls.rate_per_second<>excluded.rate_per_second) THEN excluded.window_started_at ELSE event_subscription_delivery_controls.window_started_at END,
 window_count=CASE WHEN NOT excluded.paused AND (event_subscription_delivery_controls.paused OR event_subscription_delivery_controls.rate_per_second<>excluded.rate_per_second) THEN 0 ELSE event_subscription_delivery_controls.window_count END,
 paused_at=CASE WHEN NOT excluded.paused THEN NULL WHEN event_subscription_delivery_controls.paused THEN event_subscription_delivery_controls.paused_at ELSE excluded.paused_at END,
 updated_at=excluded.updated_at
WHERE event_subscription_delivery_controls.account_id=excluded.account_id AND event_subscription_delivery_controls.app_id=excluded.app_id;

-- name: EventSubscriptionControlUsePermit :exec
UPDATE event_subscription_delivery_controls SET
 window_started_at=CASE WHEN window_started_at+interval '1 second'<=sqlc.arg(now_at)::timestamptz THEN sqlc.arg(now_at)::timestamptz ELSE window_started_at END,
 window_count=CASE WHEN window_started_at+interval '1 second'<=sqlc.arg(now_at)::timestamptz THEN 1 ELSE window_count+1 END
WHERE subscription_id=sqlc.arg(subscription_id)::uuid;

-- name: EventSubscriptionControlStats :one
SELECT count(*) FILTER (WHERE routing_state='pending')::bigint AS pending_recipients,
 count(*) FILTER (WHERE routing_state='processing')::bigint AS processing_recipients,
 min(accepted_at) FILTER (WHERE routing_state='pending')::timestamptz AS oldest_pending_at
FROM event_routing_backlog WHERE account_id=sqlc.arg(account_id)::uuid AND app_id=sqlc.arg(app_id)::uuid AND subscription_id=sqlc.arg(subscription_id)::text;
