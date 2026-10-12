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

-- name: EventConsumerExecutionCoverage :one
SELECT started_at,baseline_known,lost_at,loss_reason,
 EXISTS (SELECT 1 FROM invocation_attempt_history h WHERE h.account_id=sqlc.arg(account_id)::uuid
  AND h.app_id=sqlc.arg(app_id)::uuid AND h.finished_at>=sqlc.arg(since_at)::timestamptz
  AND h.finished_at<=sqlc.arg(now_at)::timestamptz AND h.retain_until<=sqlc.arg(now_at)::timestamptz) AS attempts_expired
FROM event_execution_coverage WHERE account_id=sqlc.arg(account_id)::uuid
 AND app_id=sqlc.arg(app_id)::uuid AND subscription_id=sqlc.arg(subscription_id)::uuid;

-- name: AssociateEventExecutionRoot :one
WITH baseline AS (
 INSERT INTO event_execution_coverage(subscription_id,account_id,app_id,started_at,baseline_known,ledger_started_at,ledger_baseline_known)
 VALUES(sqlc.arg(subscription_id)::uuid,sqlc.arg(account_id)::uuid,sqlc.arg(app_id)::uuid,clock_timestamp(),false,clock_timestamp(),false)
 ON CONFLICT DO NOTHING RETURNING subscription_id
), owner AS (
 SELECT subscription_id FROM baseline UNION ALL
 SELECT subscription_id FROM event_execution_coverage WHERE subscription_id=sqlc.arg(subscription_id)::uuid
 AND account_id=sqlc.arg(account_id)::uuid AND app_id=sqlc.arg(app_id)::uuid
), associated AS (
 INSERT INTO event_execution_roots(root_invocation_id,root_created_at,subscription_id,account_id,app_id,accepted_at,outbox_id,retention_seconds)
 SELECT i.id,i.created_at,owner.subscription_id,i.account_id,i.app_id,sqlc.arg(accepted_at)::timestamptz,
 sqlc.arg(outbox_id)::bigint,sqlc.arg(retention_seconds)::integer
 FROM invocations i CROSS JOIN owner WHERE i.id=sqlc.arg(invocation_id)::uuid AND i.account_id=sqlc.arg(account_id)::uuid AND i.app_id=sqlc.arg(app_id)::uuid
 ON CONFLICT(root_invocation_id,root_created_at) DO UPDATE SET root_invocation_id=excluded.root_invocation_id
 WHERE event_execution_roots.subscription_id=excluded.subscription_id AND event_execution_roots.account_id=excluded.account_id
 AND event_execution_roots.app_id=excluded.app_id AND event_execution_roots.accepted_at=excluded.accepted_at
 AND event_execution_roots.outbox_id=excluded.outbox_id AND event_execution_roots.retention_seconds=excluded.retention_seconds
 RETURNING root_invocation_id
)
SELECT EXISTS(SELECT 1 FROM invocations WHERE id=sqlc.arg(invocation_id)::uuid) AS invocation_exists,
 EXISTS(SELECT 1 FROM associated) AS associated;

-- name: CaptureAssociatedEventCompletion :exec
SELECT record_event_execution_outcome(i.id,i.created_at,i.replay_root_invocation_id,i.replay_root_created_at,
 i.account_id,i.app_id,i.replay_generation,0,'completion',i.completed_at,'succeeded')
FROM invocations i WHERE i.id=sqlc.arg(invocation_id)::uuid AND i.state='completed' AND i.completed_at IS NOT NULL;

-- name: CaptureAssociatedEventAttempts :exec
SELECT record_event_execution_outcome(h.invocation_id,child.created_at,h.root_invocation_id,h.root_created_at,
 h.account_id,h.app_id,h.replay_generation,h.attempt,'attempt',h.finished_at,h.outcome)
FROM invocation_attempt_history h JOIN invocations i ON i.id=sqlc.arg(invocation_id)::uuid
JOIN invocations child ON child.id=h.invocation_id AND child.account_id=i.account_id AND child.app_id=i.app_id AND child.replay_generation=h.replay_generation
WHERE h.root_invocation_id=i.id AND h.root_created_at=i.created_at AND h.account_id=i.account_id AND h.app_id=i.app_id
 AND h.finished_at IS NOT NULL AND h.started_at>=child.created_at AND h.finished_at>=child.created_at AND h.retain_until>clock_timestamp();

-- name: EventExecutionLedgerWindow :one
SELECT c.ledger_started_at,c.ledger_baseline_known,c.ledger_lost_through_at,
 count(*) FILTER (WHERE o.kind='attempt' AND o.outcome='succeeded' AND o.retain_until>sqlc.arg(now_at)::timestamptz)::bigint AS successful_attempts,
 count(*) FILTER (WHERE o.kind='attempt' AND o.outcome IN ('retry','failed','dead_letter') AND o.retain_until>sqlc.arg(now_at)::timestamptz)::bigint AS failed_attempts,
 count(*) FILTER (WHERE o.kind='attempt' AND o.outcome='unknown' AND o.retain_until>sqlc.arg(now_at)::timestamptz)::bigint AS unknown_attempts,
 count(*) FILTER (WHERE o.kind='attempt' AND o.outcome='dead_letter' AND o.retain_until>sqlc.arg(now_at)::timestamptz)::bigint AS dead_letters,
 count(*) FILTER (WHERE o.kind='completion' AND o.retain_until>sqlc.arg(now_at)::timestamptz)::bigint AS completions,
 coalesce(percentile_cont(0.95) WITHIN GROUP (ORDER BY o.latency_seconds) FILTER (WHERE o.kind='completion' AND o.retain_until>sqlc.arg(now_at)::timestamptz),0)::double precision AS completion_latency_p95_seconds,
 (max(o.finished_at) FILTER (WHERE o.retain_until<=sqlc.arg(now_at)::timestamptz))::timestamptz AS expired_through_at,
 (min(o.retain_until) FILTER (WHERE o.retain_until>sqlc.arg(now_at)::timestamptz))::timestamptz AS retain_until
FROM event_execution_coverage c LEFT JOIN event_execution_outcomes o ON o.subscription_id=c.subscription_id
 AND o.account_id=c.account_id AND o.app_id=c.app_id
 AND o.finished_at>=sqlc.arg(since_at)::timestamptz AND o.finished_at<=sqlc.arg(now_at)::timestamptz
WHERE c.account_id=sqlc.arg(account_id)::uuid AND c.app_id=sqlc.arg(app_id)::uuid AND c.subscription_id=sqlc.arg(subscription_id)::uuid
GROUP BY c.subscription_id;

-- name: PruneEventExecutionOutcomes :execrows
DELETE FROM event_execution_outcomes WHERE (invocation_id,invocation_created_at,replay_generation,attempt,kind) IN (
 SELECT invocation_id,invocation_created_at,replay_generation,attempt,kind FROM event_execution_outcomes
 WHERE retain_until<=sqlc.arg(now_at)::timestamptz ORDER BY retain_until,invocation_id
 FOR UPDATE SKIP LOCKED LIMIT sqlc.arg(batch_limit)::integer
);

-- name: PruneEventExecutionRoots :execrows
DELETE FROM event_execution_roots r WHERE (r.root_invocation_id,r.root_created_at) IN (
 SELECT r.root_invocation_id,r.root_created_at FROM event_execution_roots r
 WHERE r.root_created_at<=sqlc.arg(before_at)::timestamptz
 AND NOT EXISTS (SELECT 1 FROM event_execution_outcomes o WHERE o.root_invocation_id=r.root_invocation_id AND o.root_created_at=r.root_created_at)
 AND NOT EXISTS (SELECT 1 FROM invocations i WHERE i.account_id=r.account_id AND i.app_id=r.app_id
  AND ((i.id=r.root_invocation_id AND i.created_at=r.root_created_at) OR (i.replay_root_invocation_id=r.root_invocation_id AND i.replay_root_created_at=r.root_created_at) OR i.replayed_from_invocation_id=r.root_invocation_id))
 AND NOT EXISTS (SELECT 1 FROM event_fanout_outbox o WHERE o.id=r.outbox_id)
 AND NOT EXISTS (SELECT 1 FROM event_recovery_jobs j WHERE j.account_id=r.account_id AND j.app_id=r.app_id AND (j.state IN ('running','paused') OR (j.selection->>'mode'='execution' AND j.execution_finished_at IS NULL)))
 ORDER BY r.root_created_at,r.root_invocation_id FOR UPDATE OF r SKIP LOCKED LIMIT sqlc.arg(batch_limit)::integer
);
