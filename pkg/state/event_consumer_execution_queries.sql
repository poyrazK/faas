-- name: EventConsumerExecutionTarget :one
SELECT a.id FROM apps a WHERE a.id=sqlc.arg(app_id)::uuid AND a.account_id=sqlc.arg(account_id)::uuid AND a.status<>'deleted'
 AND (EXISTS (SELECT 1 FROM event_subscriptions s WHERE s.id=sqlc.arg(subscription_id)::uuid AND s.app_id=a.id AND s.account_id=a.account_id)
 OR EXISTS (SELECT 1 FROM event_subscription_delivery_controls c WHERE c.subscription_id=sqlc.arg(subscription_id)::uuid AND c.app_id=a.id AND c.account_id=a.account_id));


-- name: EventConsumerExecutionRoots :many
-- Admission roots include settled receipts and materialized backfill recipients.
SELECT o.id AS outbox_id,o.created_at AS accepted_at,o.source,o.event_id,
 coalesce(o.payload->>'accountid',o.payload->>'account_id',o.account_id::text)::text AS invocation_account_id
FROM event_fanout_outbox o
CROSS JOIN LATERAL (
 SELECT captured.recipient,'acceptance'::text AS origin FROM jsonb_array_elements(coalesce(o.recipient_snapshot,'[]'::jsonb)) captured(recipient)
 UNION ALL
 SELECT added.recipient,'backfill'::text FROM event_fanout_recipients added WHERE added.outbox_id=o.id AND added.receipt_position IS NOT NULL
) s
LEFT JOIN event_fanout_recipients r ON r.outbox_id=o.id AND r.subscription_id=s.recipient->>'id'
WHERE o.account_id=sqlc.arg(account_id)::uuid AND s.recipient->>'app_id'=sqlc.arg(app_id)::uuid::text
 AND s.recipient->>'id'=sqlc.arg(subscription_id)::text AND NOT s.recipient ? 'workflow' AND NOT s.recipient ? 'object_notification'
 AND CASE WHEN s.origin='backfill' OR o.recipient_claims THEN coalesce(r.state,o.recipient_progress->(s.recipient->>'id')->>'state','pending') ELSE coalesce(o.recipient_progress->(s.recipient->>'id')->>'state','pending') END='enqueued'
 AND o.created_at<=sqlc.arg(now_at)::timestamptz
ORDER BY o.created_at DESC,o.id DESC LIMIT sqlc.arg(root_limit)::integer;

-- name: EventConsumerExecutionInvocations :many
SELECT id,state,attempts,completed_at,coalesce(replay_root_invocation_id::text,'')::text AS replay_root,
 coalesce(replayed_from_invocation_id::text,'')::text AS replay_parent
FROM invocations WHERE account_id=sqlc.arg(account_id)::uuid AND app_id=sqlc.arg(app_id)::uuid
 AND created_at<=sqlc.arg(now_at)::timestamptz
 AND (id=ANY(sqlc.arg(roots)::uuid[]) OR replay_root_invocation_id=ANY(sqlc.arg(roots)::uuid[]) OR replayed_from_invocation_id=ANY(sqlc.arg(roots)::uuid[]))
ORDER BY created_at DESC,id DESC LIMIT sqlc.arg(invocation_limit)::integer;

-- name: EventConsumerExecutionAttempts :one
SELECT count(*) FILTER (WHERE outcome='succeeded')::bigint AS successful_attempts,
 count(*) FILTER (WHERE outcome IN ('retry','failed','dead_letter'))::bigint AS failed_attempts,
 count(*) FILTER (WHERE outcome='unknown')::bigint AS unknown_attempts,
 count(*) FILTER (WHERE outcome='dead_letter')::bigint AS dead_letter_attempts
FROM invocation_attempt_history
WHERE account_id=sqlc.arg(account_id)::uuid AND app_id=sqlc.arg(app_id)::uuid AND root_invocation_id=ANY(sqlc.arg(roots)::uuid[])
 AND finished_at>=sqlc.arg(since_at)::timestamptz AND finished_at<=sqlc.arg(now_at)::timestamptz
 AND retain_until>sqlc.arg(now_at)::timestamptz;
