-- name: EventRoutingRetryTarget :one
SELECT s.* FROM event_subscriptions s JOIN apps a ON a.id=s.app_id AND a.account_id=s.account_id
WHERE s.id=sqlc.arg(subscription_id)::uuid AND s.app_id=sqlc.arg(app_id)::uuid AND s.account_id=sqlc.arg(account_id)::uuid AND a.status<>'deleted';

-- name: EventRoutingRetryLockTarget :one
SELECT s.* FROM event_subscriptions s JOIN apps a ON a.id=s.app_id AND a.account_id=s.account_id
WHERE s.id=sqlc.arg(subscription_id)::uuid AND s.app_id=sqlc.arg(app_id)::uuid AND s.account_id=sqlc.arg(account_id)::uuid AND a.status<>'deleted'
FOR UPDATE OF s FOR SHARE OF a;

-- name: EventRoutingRetrySet :exec
UPDATE event_subscriptions SET routing_retry_policy=sqlc.narg(policy)::jsonb,updated_at=clock_timestamp()
WHERE id=sqlc.arg(subscription_id)::uuid;

-- name: EventRetryFinishReceipt :execrows
UPDATE event_fanout_outbox o SET state='pending',claim_token=NULL,lease_until=NULL,last_error=left(sqlc.arg(last_error)::text,1024),
available_at=least((SELECT min(event_recipient_delivery_deadline(r,o.created_at,o.recipient_progress->(r->>'id'))) FROM jsonb_array_elements(o.recipient_snapshot) r WHERE coalesce(o.recipient_progress->(r->>'id')->>'state','pending')='pending'),coalesce((SELECT min((p.value->>'next_attempt_at')::timestamptz) FROM jsonb_each(o.recipient_progress) p
 WHERE p.value->>'state'='pending' AND p.value ? 'next_attempt_at'),
 clock_timestamp()+make_interval(secs=>least(300,5*(1<<least(o.attempts-1,6))))))
WHERE o.id=sqlc.arg(id)::bigint AND o.claim_token=sqlc.arg(claim_token)::uuid AND o.state='processing';

-- name: EventAgeOrderedBinding :one
SELECT EXISTS(SELECT 1 FROM event_subscription_work_bindings WHERE subscription_id=sqlc.arg(subscription_id)::uuid AND ordered) AS ordered;

-- name: EventAgeSubscriptionPolicy :one
SELECT routing_retry_policy FROM event_subscriptions WHERE id=sqlc.arg(subscription_id)::uuid AND app_id=sqlc.arg(app_id)::uuid;

-- name: EventAgeReplayTarget :one
SELECT o.created_at, coalesce(r.recipient,(SELECT s FROM jsonb_array_elements(o.recipient_snapshot) s WHERE s->>'id'=sqlc.arg(subscription_id)::text LIMIT 1))::jsonb AS recipient
FROM event_fanout_outbox o LEFT JOIN event_fanout_recipients r ON r.outbox_id=o.id AND r.subscription_id=sqlc.arg(subscription_id)::text
WHERE o.id=sqlc.arg(outbox_id)::bigint;

-- name: EventAgeScopedReplayTarget :one
SELECT o.created_at,r.recipient FROM event_fanout_outbox o
JOIN event_fanout_recipients r ON r.outbox_id=o.id
WHERE o.account_id=sqlc.arg(account_id)::uuid AND r.app_id=sqlc.arg(app_id)::uuid AND o.source=sqlc.arg(event_source)::text AND o.event_id=sqlc.arg(event_id)::text AND r.subscription_id=sqlc.arg(subscription_id)::text;
