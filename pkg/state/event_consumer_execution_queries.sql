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

-- name: RequestEventExecutionReconciliation :exec
INSERT INTO event_execution_coverage(subscription_id,account_id,app_id,started_at,baseline_known,ledger_started_at,ledger_baseline_known,reconciliation_requested_at)
SELECT sqlc.arg(subscription_id)::uuid,sqlc.arg(account_id)::uuid,sqlc.arg(app_id)::uuid,clock_timestamp(),false,clock_timestamp(),false,clock_timestamp()
ON CONFLICT(subscription_id) DO UPDATE SET reconciliation_requested_at=coalesce(event_execution_coverage.reconciliation_requested_at,excluded.reconciliation_requested_at)
WHERE event_execution_coverage.account_id=excluded.account_id AND event_execution_coverage.app_id=excluded.app_id AND NOT event_execution_coverage.ledger_baseline_known;

-- name: GetEventExecutionReconciliation :one
SELECT coalesce(c.ledger_baseline_known,false)::boolean AS ledger_baseline_known,c.ledger_started_at,c.reconciliation_requested_at,c.lineage_repair_progress,
 EXISTS(SELECT 1 FROM invocations i WHERE i.account_id=sqlc.arg(account_id)::uuid AND i.app_id=sqlc.arg(app_id)::uuid
 AND ((i.replay_root_invocation_id IS NULL)<>(i.replay_root_created_at IS NULL) OR NOT EXISTS(SELECT 1 FROM event_execution_roots r
 WHERE r.root_invocation_id=coalesce(i.replay_root_invocation_id,i.id) AND r.root_created_at=coalesce(i.replay_root_created_at,i.created_at)
 AND r.account_id=i.account_id AND r.app_id=i.app_id))) AS unattributed_execution_present
FROM (SELECT sqlc.arg(subscription_id)::uuid AS subscription_id) target
LEFT JOIN event_execution_coverage c ON c.subscription_id=target.subscription_id
 AND c.account_id=sqlc.arg(account_id)::uuid AND c.app_id=sqlc.arg(app_id)::uuid;

-- name: AdvanceEventExecutionReconciliations :execrows
WITH pending AS (
 SELECT c.subscription_id FROM event_execution_coverage c JOIN apps a ON a.id=c.app_id AND a.account_id=c.account_id AND a.status<>'deleted'
 WHERE c.reconciliation_requested_at IS NOT NULL AND NOT c.ledger_baseline_known
 AND NOT EXISTS(SELECT 1 FROM invocations i WHERE i.account_id=c.account_id AND i.app_id=c.app_id
 AND ((i.replay_root_invocation_id IS NULL)<>(i.replay_root_created_at IS NULL) OR NOT EXISTS(SELECT 1 FROM event_execution_roots r
 WHERE r.root_invocation_id=coalesce(i.replay_root_invocation_id,i.id) AND r.root_created_at=coalesce(i.replay_root_created_at,i.created_at)
 AND r.account_id=i.account_id AND r.app_id=i.app_id)))
 ORDER BY c.reconciliation_requested_at,c.subscription_id
 FOR UPDATE OF c SKIP LOCKED LIMIT sqlc.arg(batch_limit)::integer
), ready AS (
 SELECT c.subscription_id FROM event_execution_coverage c JOIN pending p USING(subscription_id)
 WHERE NOT EXISTS(SELECT 1 FROM invocations i WHERE i.account_id=c.account_id AND i.app_id=c.app_id
 AND ((i.replay_root_invocation_id IS NULL)<>(i.replay_root_created_at IS NULL) OR NOT EXISTS(SELECT 1 FROM event_execution_roots r
 WHERE r.root_invocation_id=coalesce(i.replay_root_invocation_id,i.id) AND r.root_created_at=coalesce(i.replay_root_created_at,i.created_at)
 AND r.account_id=i.account_id AND r.app_id=i.app_id)))
)
UPDATE event_execution_coverage c SET ledger_started_at=greatest(clock_timestamp(),c.ledger_started_at,c.reconciliation_requested_at,c.ledger_lost_through_at+interval '1 microsecond'),ledger_baseline_known=true
FROM ready WHERE c.subscription_id=ready.subscription_id;

-- name: EventExecutionLineageRepairReceipts :many
-- Admission roots include settled receipts and materialized backfill recipients.
SELECT o.payload, o.id AS outbox_id,o.created_at AS accepted_at,o.source,o.event_id,
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
 AND o.id>sqlc.arg(after_outbox_id)::bigint
ORDER BY o.id ASC LIMIT sqlc.arg(root_limit)::integer FOR SHARE OF o;


-- name: EventExecutionLineageRepairJobs :many
SELECT c.subscription_id,c.account_id,c.app_id,c.lineage_repair_progress FROM event_execution_coverage c
JOIN apps a ON a.id=c.app_id AND a.account_id=c.account_id AND a.status<>'deleted'
WHERE c.reconciliation_requested_at IS NOT NULL AND NOT c.ledger_baseline_known
AND EXISTS(SELECT 1 FROM event_fanout_outbox o WHERE o.account_id=c.account_id
 AND o.id>coalesce((c.lineage_repair_progress->>'cursor')::bigint,0)
 AND (EXISTS(SELECT 1 FROM jsonb_array_elements(coalesce(o.recipient_snapshot,'[]'::jsonb)) r WHERE r->>'id'=c.subscription_id::text AND r->>'app_id'=c.app_id::text)
 OR EXISTS(SELECT 1 FROM event_fanout_recipients r WHERE r.outbox_id=o.id AND r.subscription_id=c.subscription_id::text AND r.recipient->>'app_id'=c.app_id::text)))
ORDER BY coalesce((c.lineage_repair_progress->>'last_scan')::timestamptz,c.reconciliation_requested_at),c.subscription_id
FOR UPDATE OF c SKIP LOCKED LIMIT sqlc.arg(job_limit)::integer;

-- name: EventExecutionLineageRepairRoot :one
SELECT id,created_at,replay_root_invocation_id,replay_root_created_at,source,method,path,payload FROM invocations
WHERE id=sqlc.arg(invocation_id)::uuid AND account_id=sqlc.arg(account_id)::uuid AND app_id=sqlc.arg(app_id)::uuid FOR UPDATE;

-- name: EventExecutionLineageRepairExistingRoot :one
SELECT subscription_id,account_id,app_id,accepted_at,outbox_id FROM event_execution_roots
WHERE root_invocation_id=sqlc.arg(invocation_id)::uuid AND root_created_at=sqlc.arg(created_at)::timestamptz;

-- name: EventExecutionLineageRepairAmbiguous :one
SELECT EXISTS(SELECT 1 FROM event_fanout_outbox o
WHERE o.account_id=sqlc.arg(account_id)::uuid AND o.source=sqlc.arg(event_source)::text AND o.event_id=sqlc.arg(event_id)::text
 AND coalesce(o.payload->>'accountid',o.payload->>'account_id',o.account_id::text)=sqlc.arg(invocation_account_id)::text
 AND o.id<>sqlc.arg(outbox_id)::bigint AND o.created_at<=sqlc.arg(created_at)::timestamptz
 AND (EXISTS(SELECT 1 FROM jsonb_array_elements(coalesce(o.recipient_snapshot,'[]'::jsonb)) r WHERE r->>'id'=sqlc.arg(subscription_id)::text AND r->>'app_id'=sqlc.arg(app_id)::uuid::text)
 OR EXISTS(SELECT 1 FROM event_fanout_recipients r WHERE r.outbox_id=o.id AND r.subscription_id=sqlc.arg(subscription_id)::text AND r.recipient->>'app_id'=sqlc.arg(app_id)::uuid::text))) AS ambiguous;

-- name: SaveEventExecutionLineageRepairProgress :exec
UPDATE event_execution_coverage SET lineage_repair_progress=lineage_repair_progress || (sqlc.arg(progress)::jsonb - 'case_backfill') || jsonb_build_object('last_scan',clock_timestamp())
WHERE subscription_id=sqlc.arg(subscription_id)::uuid AND account_id=sqlc.arg(account_id)::uuid AND app_id=sqlc.arg(app_id)::uuid;

-- name: EventExecutionLineageCaseReceipt :many
-- Admission roots include settled receipts and materialized backfill recipients.
SELECT o.payload, o.id AS outbox_id,o.created_at AS accepted_at,o.source,o.event_id,
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
 AND o.id=sqlc.arg(outbox_id)::bigint
ORDER BY o.id ASC LIMIT 1 FOR SHARE OF o;


-- name: RecordEventExecutionLineageCase :execrows
INSERT INTO event_execution_lineage_cases(subscription_id,outbox_id,account_id,app_id,accepted_at,root_invocation_id,root_created_at,status,reason,recorded_at,retain_until)
VALUES(sqlc.arg(subscription_id)::uuid,sqlc.arg(outbox_id)::bigint,sqlc.arg(account_id)::uuid,sqlc.arg(app_id)::uuid,sqlc.arg(accepted_at)::timestamptz,sqlc.arg(root_invocation_id)::text,sqlc.narg(root_created_at)::timestamptz,sqlc.arg(status)::text,sqlc.arg(reason)::text,clock_timestamp(),clock_timestamp()+make_interval(secs=>sqlc.arg(retention_seconds)::integer))
ON CONFLICT(subscription_id,outbox_id) DO UPDATE SET status=excluded.status,reason=excluded.reason,revision=event_execution_lineage_cases.revision+1,root_created_at=coalesce(event_execution_lineage_cases.root_created_at,excluded.root_created_at),recorded_at=excluded.recorded_at,retain_until=greatest(event_execution_lineage_cases.retain_until,excluded.retain_until)
WHERE event_execution_lineage_cases.account_id=excluded.account_id AND event_execution_lineage_cases.app_id=excluded.app_id AND event_execution_lineage_cases.accepted_at=excluded.accepted_at;

-- name: ListEventExecutionLineageCases :many
SELECT * FROM event_execution_lineage_cases WHERE account_id=sqlc.arg(account_id)::uuid AND app_id=sqlc.arg(app_id)::uuid AND subscription_id=sqlc.arg(subscription_id)::uuid AND outbox_id>sqlc.arg(after_outbox_id)::bigint AND retain_until>clock_timestamp()
ORDER BY outbox_id LIMIT sqlc.arg(page_limit)::integer;

-- name: LockEventExecutionLineageCase :one
SELECT * FROM event_execution_lineage_cases WHERE account_id=sqlc.arg(account_id)::uuid AND app_id=sqlc.arg(app_id)::uuid AND subscription_id=sqlc.arg(subscription_id)::uuid AND outbox_id=sqlc.arg(outbox_id)::bigint AND retain_until>clock_timestamp() FOR UPDATE;

-- name: LockEventExecutionLineageCoverage :one
SELECT subscription_id,account_id,app_id,lineage_repair_progress FROM event_execution_coverage
WHERE account_id=sqlc.arg(account_id)::uuid AND app_id=sqlc.arg(app_id)::uuid AND subscription_id=sqlc.arg(subscription_id)::uuid FOR UPDATE;

-- name: EventExecutionLineageRetryResult :one
SELECT expected_revision,result FROM event_execution_lineage_retries WHERE subscription_id=sqlc.arg(subscription_id)::uuid AND outbox_id=sqlc.arg(outbox_id)::bigint AND request_id=sqlc.arg(request_id)::uuid AND retain_until>clock_timestamp();

-- name: UpdateEventExecutionLineageCaseRetry :one
UPDATE event_execution_lineage_cases SET root_created_at=coalesce(root_created_at,sqlc.narg(root_created_at)::timestamptz),status=sqlc.arg(status)::text,reason=sqlc.arg(reason)::text,revision=revision+1,retry_count=retry_count+1,recorded_at=clock_timestamp(),retain_until=greatest(retain_until,clock_timestamp()+make_interval(secs=>sqlc.arg(retention_seconds)::integer))
WHERE subscription_id=sqlc.arg(subscription_id)::uuid AND outbox_id=sqlc.arg(outbox_id)::bigint RETURNING *;

-- name: SaveEventExecutionLineageRetry :exec
INSERT INTO event_execution_lineage_retries(subscription_id,outbox_id,request_id,expected_revision,result,retain_until)
VALUES(sqlc.arg(subscription_id)::uuid,sqlc.arg(outbox_id)::bigint,sqlc.arg(request_id)::uuid,sqlc.arg(expected_revision)::bigint,sqlc.arg(result)::jsonb,sqlc.arg(retain_until)::timestamptz);

-- name: PruneEventExecutionLineageCases :execrows
DELETE FROM event_execution_lineage_cases WHERE (subscription_id,outbox_id) IN (SELECT subscription_id,outbox_id FROM event_execution_lineage_cases c WHERE retain_until<=sqlc.arg(now_at)::timestamptz AND NOT EXISTS(SELECT 1 FROM event_execution_lineage_retries r WHERE r.subscription_id=c.subscription_id AND r.outbox_id=c.outbox_id) ORDER BY retain_until FOR UPDATE SKIP LOCKED LIMIT sqlc.arg(batch_limit)::integer);
-- name: PruneEventExecutionLineageRetries :execrows
DELETE FROM event_execution_lineage_retries WHERE (subscription_id,outbox_id,request_id) IN (SELECT subscription_id,outbox_id,request_id FROM event_execution_lineage_retries WHERE retain_until<=sqlc.arg(now_at)::timestamptz ORDER BY retain_until FOR UPDATE SKIP LOCKED LIMIT sqlc.arg(batch_limit)::integer);

-- name: PreviewEventExecutionLineageRetries :many
SELECT * FROM event_execution_lineage_cases WHERE account_id=sqlc.arg(account_id)::uuid AND app_id=sqlc.arg(app_id)::uuid
 AND subscription_id=sqlc.arg(subscription_id)::uuid AND outbox_id>sqlc.arg(after_outbox_id)::bigint AND retain_until>clock_timestamp()
 AND status IN ('unresolved','conflict') AND (sqlc.arg(reason)::text='' OR reason=sqlc.arg(reason)::text)
ORDER BY outbox_id LIMIT sqlc.arg(page_limit)::integer;

-- name: GetEventExecutionLineageRetryJob :one
SELECT * FROM event_execution_lineage_retry_jobs WHERE account_id=sqlc.arg(account_id)::uuid AND app_id=sqlc.arg(app_id)::uuid
 AND subscription_id=sqlc.arg(subscription_id)::uuid AND request_id=sqlc.arg(request_id)::uuid AND retain_until>clock_timestamp();

-- name: CreateEventExecutionLineageRetryJob :exec
INSERT INTO event_execution_lineage_retry_jobs(subscription_id,request_id,account_id,app_id,state,snapshot,created_at,updated_at,retain_until)
VALUES(sqlc.arg(subscription_id)::uuid,sqlc.arg(request_id)::uuid,sqlc.arg(account_id)::uuid,sqlc.arg(app_id)::uuid,'pending',sqlc.arg(snapshot)::jsonb,sqlc.arg(created_at)::timestamptz,sqlc.arg(created_at)::timestamptz,sqlc.arg(retain_until)::timestamptz);

-- name: LockNextEventExecutionLineageRetryJob :one
SELECT j.* FROM event_execution_lineage_retry_jobs j JOIN apps a ON a.id=j.app_id AND a.account_id=j.account_id AND a.status<>'deleted'
WHERE j.state='pending' AND j.retain_until>clock_timestamp()
ORDER BY j.updated_at,j.subscription_id,j.request_id FOR UPDATE OF j SKIP LOCKED LIMIT 1;

-- name: SaveEventExecutionLineageRetryJob :exec
UPDATE event_execution_lineage_retry_jobs SET state=sqlc.arg(state)::text,snapshot=sqlc.arg(snapshot)::jsonb,updated_at=clock_timestamp()
WHERE subscription_id=sqlc.arg(subscription_id)::uuid AND request_id=sqlc.arg(request_id)::uuid;

-- name: PruneEventExecutionLineageRetryJobs :execrows
DELETE FROM event_execution_lineage_retry_jobs WHERE (subscription_id,request_id) IN (
 SELECT subscription_id,request_id FROM event_execution_lineage_retry_jobs WHERE retain_until<=sqlc.arg(now_at)::timestamptz
 ORDER BY retain_until FOR UPDATE SKIP LOCKED LIMIT sqlc.arg(batch_limit)::integer);

-- name: EventExecutionLineageRetryJobIdentity :one
SELECT * FROM event_execution_lineage_retry_jobs WHERE account_id=sqlc.arg(account_id)::uuid AND app_id=sqlc.arg(app_id)::uuid
 AND subscription_id=sqlc.arg(subscription_id)::uuid AND request_id=sqlc.arg(request_id)::uuid;

-- name: LockEventExecutionLineageRetryJob :one
SELECT * FROM event_execution_lineage_retry_jobs WHERE account_id=sqlc.arg(account_id)::uuid AND app_id=sqlc.arg(app_id)::uuid
 AND subscription_id=sqlc.arg(subscription_id)::uuid AND request_id=sqlc.arg(request_id)::uuid AND retain_until>clock_timestamp()
FOR UPDATE;

-- name: EventExecutionLineageBackfillReceipts :many
-- Admission roots include settled receipts and materialized backfill recipients.
SELECT o.payload, o.id AS outbox_id,o.created_at AS accepted_at,o.source,o.event_id,
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
 AND o.id>sqlc.arg(after_outbox_id)::bigint AND o.id<=sqlc.arg(through_outbox_id)::bigint
ORDER BY o.id ASC LIMIT sqlc.arg(root_limit)::integer FOR SHARE OF o;


-- name: GetEventExecutionLineageBackfillProgress :one
SELECT lineage_repair_progress FROM event_execution_coverage WHERE account_id=sqlc.arg(account_id)::uuid AND app_id=sqlc.arg(app_id)::uuid AND subscription_id=sqlc.arg(subscription_id)::uuid;

-- name: EventExecutionLineageBackfillUpper :one
SELECT coalesce(max(id),0)::bigint FROM event_fanout_outbox WHERE account_id=sqlc.arg(account_id)::uuid;

-- name: SaveEventExecutionLineageBackfill :exec
UPDATE event_execution_coverage SET lineage_repair_progress=jsonb_set(lineage_repair_progress,'{case_backfill}',sqlc.arg(backfill)::jsonb,true)
WHERE account_id=sqlc.arg(account_id)::uuid AND app_id=sqlc.arg(app_id)::uuid AND subscription_id=sqlc.arg(subscription_id)::uuid;

-- name: EventExecutionLineageBackfillJobs :many
SELECT c.subscription_id,c.account_id,c.app_id,c.lineage_repair_progress FROM event_execution_coverage c
JOIN apps a ON a.id=c.app_id AND a.account_id=c.account_id AND a.status<>'deleted'
WHERE c.lineage_repair_progress->'case_backfill'->>'status' IN ('pending','waiting_for_retry_jobs')
ORDER BY (c.lineage_repair_progress->'case_backfill'->>'updated_at')::timestamptz,c.subscription_id
FOR UPDATE OF c SKIP LOCKED LIMIT sqlc.arg(job_limit)::integer;

-- name: EventExecutionLineageBackfillRetryPending :one
SELECT EXISTS(SELECT 1 FROM event_execution_lineage_retry_jobs WHERE account_id=sqlc.arg(account_id)::uuid AND app_id=sqlc.arg(app_id)::uuid AND subscription_id=sqlc.arg(subscription_id)::uuid AND state IN ('pending','paused') AND retain_until>clock_timestamp());

-- name: EventExecutionLineageBackfillCaseExists :one
SELECT EXISTS(SELECT 1 FROM event_execution_lineage_cases WHERE account_id=sqlc.arg(account_id)::uuid AND app_id=sqlc.arg(app_id)::uuid AND subscription_id=sqlc.arg(subscription_id)::uuid AND outbox_id=sqlc.arg(outbox_id)::bigint);

-- name: EventExecutionLineageBackfillAssociated :one
SELECT EXISTS(SELECT 1 FROM event_execution_roots WHERE root_invocation_id=sqlc.arg(invocation_id)::uuid AND account_id=sqlc.arg(account_id)::uuid AND app_id=sqlc.arg(app_id)::uuid AND subscription_id=sqlc.arg(subscription_id)::uuid AND outbox_id=sqlc.arg(outbox_id)::bigint AND accepted_at=sqlc.arg(accepted_at)::timestamptz);

-- name: InsertEventExecutionLineageBackfillCase :execrows
INSERT INTO event_execution_lineage_cases(diagnostic_id,subscription_id,outbox_id,account_id,app_id,accepted_at,root_invocation_id,root_created_at,status,reason,recorded_at,retain_until)
VALUES(sqlc.arg(diagnostic_id)::uuid,sqlc.arg(subscription_id)::uuid,sqlc.arg(outbox_id)::bigint,sqlc.arg(account_id)::uuid,sqlc.arg(app_id)::uuid,sqlc.arg(accepted_at)::timestamptz,sqlc.arg(root_invocation_id)::text,sqlc.narg(root_created_at)::timestamptz,sqlc.arg(status)::text,sqlc.arg(reason)::text,clock_timestamp(),clock_timestamp()+make_interval(secs=>sqlc.arg(retention_seconds)::integer))
ON CONFLICT(subscription_id,outbox_id) DO NOTHING;

-- name: ListEventExecutionLineageRetryJobs :many
SELECT request_id,subscription_id,app_id,state,
 jsonb_array_length(snapshot->'items')::integer AS selected,
 (snapshot->>'processed')::integer AS processed,created_at,updated_at,retain_until,
 (snapshot->>'last_progress_at')::timestamptz AS last_progress_at
FROM event_execution_lineage_retry_jobs
WHERE account_id=sqlc.arg(account_id)::uuid AND app_id=sqlc.arg(app_id)::uuid
 AND subscription_id=sqlc.arg(subscription_id)::uuid AND retain_until>sqlc.arg(now_at)::timestamptz
 AND (sqlc.arg(status)::text='' OR state=sqlc.arg(status)::text)
 AND (sqlc.arg(attention_filter)::text='' OR (state IN ('pending','paused') AND created_at<=sqlc.arg(now_at)::timestamptz AND (
  (sqlc.arg(attention_filter)::text IN ('any','stalled') AND state='pending' AND updated_at<=sqlc.arg(stalled_before)::timestamptz)
  OR (sqlc.arg(attention_filter)::text IN ('any','retention_risk') AND retain_until<=sqlc.arg(retention_before)::timestamptz))))
 AND (NOT sqlc.arg(has_after)::boolean OR (created_at,request_id)<(sqlc.arg(after_created_at)::timestamptz,sqlc.arg(after_request_id)::uuid))
ORDER BY created_at DESC,request_id DESC LIMIT sqlc.arg(page_limit)::integer;

-- name: GetEventExecutionLineageRetryControlReceipt :one
SELECT * FROM event_execution_lineage_retry_control_receipts
WHERE account_id=sqlc.arg(account_id)::uuid AND app_id=sqlc.arg(app_id)::uuid
 AND subscription_id=sqlc.arg(subscription_id)::uuid AND job_request_id=sqlc.arg(job_request_id)::uuid AND command_id=sqlc.arg(command_id)::uuid;

-- name: EventExecutionLineageRetryControlSequence :one
SELECT coalesce(max(sequence),0)::bigint FROM event_execution_lineage_retry_control_receipts
WHERE subscription_id=sqlc.arg(subscription_id)::uuid AND job_request_id=sqlc.arg(job_request_id)::uuid;

-- name: RecordEventExecutionLineageRetryControlReceipt :exec
INSERT INTO event_execution_lineage_retry_control_receipts(subscription_id,job_request_id,command_id,account_id,app_id,action,expected_created_at,sequence,result,recorded_at,retain_until)
VALUES(sqlc.arg(subscription_id)::uuid,sqlc.arg(job_request_id)::uuid,sqlc.arg(command_id)::uuid,sqlc.arg(account_id)::uuid,sqlc.arg(app_id)::uuid,sqlc.arg(action)::text,sqlc.arg(expected_created_at)::timestamptz,sqlc.arg(sequence)::bigint,sqlc.arg(result)::jsonb,sqlc.arg(recorded_at)::timestamptz,sqlc.arg(retain_until)::timestamptz);

-- name: ListEventExecutionLineageRetryControlReceipts :many
SELECT * FROM event_execution_lineage_retry_control_receipts
WHERE account_id=sqlc.arg(account_id)::uuid AND app_id=sqlc.arg(app_id)::uuid
 AND subscription_id=sqlc.arg(subscription_id)::uuid AND job_request_id=sqlc.arg(job_request_id)::uuid
 AND expected_created_at=sqlc.arg(expected_created_at)::timestamptz AND retain_until>statement_timestamp()
 AND sequence>sqlc.arg(after_sequence)::bigint
 AND (sqlc.arg(action_filter)::text='' OR action=sqlc.arg(action_filter)::text)
 AND (sqlc.arg(actor_id_filter)::text='' OR result->>'actor_id'=sqlc.arg(actor_id_filter)::text)
 AND (sqlc.arg(credential_type_filter)::text='' OR result->>'credential_type'=sqlc.arg(credential_type_filter)::text)
ORDER BY sequence LIMIT sqlc.arg(page_limit)::integer;

-- name: EventExecutionLineageRetryHealthJobs :many
SELECT request_id,state,created_at,updated_at,retain_until,
 (snapshot->>'processed')::integer AS processed,
 jsonb_array_length(snapshot->'items')::integer AS selected,
 (snapshot->>'last_progress_at')::timestamptz AS last_progress_at
FROM event_execution_lineage_retry_jobs
WHERE account_id=sqlc.arg(account_id)::uuid AND app_id=sqlc.arg(app_id)::uuid
 AND subscription_id=sqlc.arg(subscription_id)::uuid AND state IN ('pending','paused')
 AND created_at<=sqlc.arg(now_at)::timestamptz AND retain_until>sqlc.arg(now_at)::timestamptz
ORDER BY retain_until,updated_at,request_id LIMIT sqlc.arg(job_limit)::integer;
