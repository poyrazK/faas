-- name: EventExecutionRecoveryRoots :many
SELECT DISTINCT o.id AS outbox_id,o.source AS event_source,o.event_id,o.event_type,
 (s.recipient->>'id')::text AS subscription_id,
 coalesce(o.payload->>'accountid',o.payload->>'account_id',o.account_id::text)::text AS invocation_account_id
FROM event_fanout_outbox o
CROSS JOIN LATERAL (
 SELECT captured.recipient,'acceptance'::text AS origin FROM jsonb_array_elements(coalesce(o.recipient_snapshot,'[]'::jsonb)) captured(recipient)
 UNION ALL
 SELECT added.recipient,'backfill'::text FROM event_fanout_recipients added WHERE added.outbox_id=o.id AND added.receipt_position IS NOT NULL
) s
LEFT JOIN event_fanout_recipients r ON r.outbox_id=o.id AND r.subscription_id=s.recipient->>'id'
WHERE o.account_id=sqlc.arg(account_id)::uuid AND s.recipient->>'app_id'=sqlc.arg(app_id)::uuid::text
 AND coalesce(s.recipient->'workflow','null'::jsonb)='null'::jsonb
 AND coalesce(s.recipient->'object_notification','null'::jsonb)='null'::jsonb
 AND CASE WHEN s.origin='backfill' OR o.recipient_claims THEN coalesce(r.state,o.recipient_progress->(s.recipient->>'id')->>'state','pending') ELSE coalesce(o.recipient_progress->(s.recipient->>'id')->>'state','pending') END='enqueued'
 AND (sqlc.arg(subscription_id)::text='' OR s.recipient->>'id'=sqlc.arg(subscription_id)::text)
 AND (sqlc.arg(event_source)::text='' OR o.source=sqlc.arg(event_source)::text)
 AND (sqlc.arg(event_type)::text='' OR o.event_type=sqlc.arg(event_type)::text)
 AND o.id<=sqlc.arg(max_outbox)::bigint
 AND (o.id,s.recipient->>'id')>(sqlc.arg(after_outbox)::bigint,sqlc.arg(after_subscription)::text)
ORDER BY 1,5 LIMIT sqlc.arg(page_limit)::integer;

-- name: EventExecutionRecoveryCandidates :many
WITH roots AS (
 SELECT (r->>'outbox_id')::bigint AS outbox_id,(r->>'event_source')::text AS event_source,
 (r->>'event_id')::text AS event_id,(r->>'event_type')::text AS event_type,
 (r->>'subscription_id')::text AS subscription_id,(r->>'root_id')::uuid AS root_id
 FROM jsonb_array_elements(sqlc.arg(roots)::jsonb) r
)
SELECT roots.outbox_id,roots.event_source,roots.event_id,roots.event_type,roots.subscription_id,
 coalesce(i.completed_at,i.created_at)::timestamptz AS failed_at,i.state::text AS failure_code,true::boolean AS retryable,
 jsonb_build_object('invocation_id',i.id::text,'state',i.state,'attempts',i.attempts,'generation',i.replay_generation,
 'created_at',i.created_at,'completed_at',i.completed_at,'dead_letter_id',coalesce(e.id::text,''))::jsonb AS expected_progress
FROM roots
CROSS JOIN LATERAL (
 SELECT inv.* FROM invocations inv WHERE inv.account_id=sqlc.arg(account_id)::uuid AND inv.app_id=sqlc.arg(app_id)::uuid
 AND (inv.id=roots.root_id OR inv.replay_root_invocation_id=roots.root_id OR inv.replayed_from_invocation_id=roots.root_id)
 ORDER BY coalesce(inv.last_replayed_at,inv.created_at) DESC,inv.created_at DESC,inv.replay_generation DESC,inv.id DESC LIMIT 1
) i
LEFT JOIN production_dead_letter_events e ON e.source='invocation' AND e.source_id=i.id AND e.account_id=i.account_id AND e.app_id=i.app_id AND e.replayed_at IS NULL
WHERE i.state IN ('failed','dead_letter') AND i.environment_id IS NULL
 AND NOT EXISTS (SELECT 1 FROM customer_operation_executions op WHERE op.invocation_id=i.id)
 AND (i.work_expires_at IS NULL OR i.work_expires_at>sqlc.arg(now_at)::timestamptz)
 AND (i.start_deadline_at IS NULL OR i.start_deadline_at>sqlc.arg(now_at)::timestamptz)
 AND (sqlc.arg(outcome)::text='' OR i.state=sqlc.arg(outcome)::text)
 AND coalesce(i.completed_at,i.created_at)<=sqlc.arg(failed_before)::timestamptz
 AND NOT EXISTS (SELECT 1 FROM invocation_plain_replays p WHERE p.parent_invocation_id=i.id)
 AND NOT EXISTS (SELECT 1 FROM invocation_keyed_replays k WHERE k.parent_invocation_id=i.id)
 AND ((i.state='dead_letter' AND e.id IS NOT NULL)
 OR (i.state='failed' AND i.queue_binding_id IS NULL AND coalesce(i.queue_name,'')=''
 AND (i.work_policy_name IS NULL OR (octet_length(i.work_key_digest)=32 AND i.work_sequence>0))))
ORDER BY 1,5;

-- name: EventExecutionRecoveryReceipt :one
SELECT o.id FROM event_fanout_outbox o
WHERE o.id=sqlc.arg(outbox_id)::bigint AND o.account_id=sqlc.arg(account_id)::uuid
 AND EXISTS (SELECT 1 FROM apps a WHERE a.id=sqlc.arg(app_id)::uuid AND a.account_id=o.account_id AND a.status<>'deleted')
 AND (EXISTS (SELECT 1 FROM jsonb_array_elements(coalesce(o.recipient_snapshot,'[]'::jsonb)) r WHERE r->>'id'=sqlc.arg(subscription_id)::text AND r->>'app_id'=sqlc.arg(app_id)::uuid::text)
 OR EXISTS (SELECT 1 FROM event_fanout_recipients r WHERE r.outbox_id=o.id AND r.subscription_id=sqlc.arg(subscription_id)::text AND r.recipient->>'app_id'=sqlc.arg(app_id)::uuid::text));

-- name: EventExecutionRecoveryBoundary :one
SELECT coalesce(max(id),0)::bigint FROM event_fanout_outbox WHERE account_id=$1;

-- name: EventRecoveryReadApp :one
SELECT id FROM apps WHERE id=sqlc.arg(app_id)::uuid AND account_id=sqlc.arg(account_id)::uuid AND status<>'deleted';
