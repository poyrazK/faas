-- name: EventCircuitGet :one
SELECT * FROM event_subscription_circuit_breakers WHERE subscription_id=sqlc.arg(subscription_id)::uuid AND account_id=sqlc.arg(account_id)::uuid AND app_id=sqlc.arg(app_id)::uuid;

-- name: EventCircuitSave :execrows
INSERT INTO event_subscription_circuit_breakers(subscription_id,account_id,app_id,policy,state_data,updated_at)
VALUES(sqlc.arg(subscription_id)::uuid,sqlc.arg(account_id)::uuid,sqlc.arg(app_id)::uuid,sqlc.arg(policy)::jsonb,sqlc.arg(state_data)::jsonb,sqlc.arg(now_at)::timestamptz)
ON CONFLICT(subscription_id) DO UPDATE SET policy=excluded.policy,state_data=excluded.state_data,updated_at=excluded.updated_at
WHERE event_subscription_circuit_breakers.account_id=excluded.account_id AND event_subscription_circuit_breakers.app_id=excluded.app_id;

-- name: EventCircuitDelete :exec
DELETE FROM event_subscription_circuit_breakers WHERE subscription_id=sqlc.arg(subscription_id)::uuid AND account_id=sqlc.arg(account_id)::uuid AND app_id=sqlc.arg(app_id)::uuid;

-- name: EventCircuitEnsureControl :exec
INSERT INTO event_subscription_delivery_controls(subscription_id,account_id,app_id)
VALUES(sqlc.arg(subscription_id)::uuid,sqlc.arg(account_id)::uuid,sqlc.arg(app_id)::uuid) ON CONFLICT DO NOTHING;

-- name: EventCircuitAppAvailable :one
SELECT EXISTS(SELECT 1 FROM apps WHERE id=sqlc.arg(app_id)::uuid AND account_id=sqlc.arg(account_id)::uuid AND status<>'deleted') AS available;

-- name: EventCircuitWindow :one
SELECT count(*) FILTER (WHERE h.state='enqueued')::bigint AS successes,
 count(*) FILTER (WHERE h.failure_code<>'delivery_expired' AND (h.state='failed' OR h.state='pending' AND h.last_error<>'' AND h.capacity_scope=''))::bigint AS failures,
 EXISTS(SELECT 1 FROM event_fanout_history_summaries s JOIN event_fanout_outbox o2 ON o2.id=s.outbox_id
 WHERE s.app_id=sqlc.arg(app_id)::uuid AND o2.account_id=sqlc.arg(account_id)::uuid AND s.subscription_id=sqlc.arg(subscription_id)::text
 AND s.compacted_through_at>=sqlc.arg(since_at)::timestamptz) AS incomplete
FROM event_fanout_attempt_history h JOIN event_fanout_outbox o ON o.id=h.outbox_id
WHERE h.app_id=sqlc.arg(app_id)::uuid AND o.account_id=sqlc.arg(account_id)::uuid AND h.subscription_id=sqlc.arg(subscription_id)::text
 AND h.occurred_at>=sqlc.arg(since_at)::timestamptz AND h.occurred_at<=sqlc.arg(now_at)::timestamptz AND h.action IN ('fanout_attempt','backfill_attempt');

-- name: EventCircuitProbeOutcome :one
SELECT (recipient_progress->sqlc.arg(subscription_id)::text)::jsonb AS progress FROM event_fanout_outbox
WHERE id=sqlc.arg(outbox_id)::bigint AND account_id=sqlc.arg(account_id)::uuid;

-- name: EventCircuitGetForApp :one
SELECT * FROM event_subscription_circuit_breakers WHERE subscription_id=sqlc.arg(subscription_id)::uuid AND app_id=sqlc.arg(app_id)::uuid;
