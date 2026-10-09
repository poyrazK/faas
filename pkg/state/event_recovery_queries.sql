-- name: EventRecoveryCandidates :many
SELECT o.id AS outbox_id, o.source AS event_source,o.event_id,o.event_type,
 (r.recipient->>'id')::text AS subscription_id,
 coalesce((p.progress->>'updated_at')::timestamptz,o.created_at)::timestamptz AS failed_at,
 coalesce(nullif(p.progress->>'failure_code',''),'unknown')::text AS failure_code,
 coalesce((p.progress->>'retryable')::boolean,false)::boolean AS retryable,
 event_recovery_failure_identity(p.progress)::jsonb AS expected_progress
FROM event_fanout_outbox o
JOIN apps a ON a.id=sqlc.arg(app_id)::uuid AND a.account_id=o.account_id AND a.status<>'deleted'
CROSS JOIN LATERAL jsonb_array_elements(coalesce(o.recipient_snapshot,'[]'::jsonb)) WITH ORDINALITY r(recipient,position)
CROSS JOIN LATERAL (SELECT o.recipient_progress->(r.recipient->>'id') AS progress) p
WHERE o.account_id=sqlc.arg(account_id)::uuid AND r.recipient->>'app_id'=a.id::text
 AND coalesce(r.recipient->'workflow','null'::jsonb)='null'::jsonb
 AND p.progress->>'state'='failed'
 AND (event_recipient_delivery_deadline(r.recipient,o.created_at,'{}'::jsonb) IS NULL OR event_recipient_delivery_deadline(r.recipient,o.created_at,'{}'::jsonb)>clock_timestamp())
 AND (sqlc.arg(include_non_retryable)::boolean OR coalesce((p.progress->>'retryable')::boolean,false))
 AND (sqlc.arg(subscription_id)::text='' OR r.recipient->>'id'=sqlc.arg(subscription_id)::text)
 AND (sqlc.arg(event_source)::text='' OR o.source=sqlc.arg(event_source)::text)
 AND (sqlc.arg(event_type)::text='' OR o.event_type=sqlc.arg(event_type)::text)
 AND (sqlc.arg(failure_code)::text='' OR coalesce(nullif(p.progress->>'failure_code',''),'unknown')=sqlc.arg(failure_code)::text)
 AND coalesce((p.progress->>'updated_at')::timestamptz,o.created_at)<=sqlc.arg(failed_before)::timestamptz
ORDER BY coalesce((r.recipient->'work'->>'routing_order')::bigint,o.id),o.id,r.position
LIMIT sqlc.arg(page_limit)::integer;

-- name: EventRecoveryActiveCount :one
SELECT count(*) FROM event_recovery_jobs WHERE account_id=sqlc.arg(account_id)::uuid AND state IN ('running','paused');

-- name: EventRecoveryCreate :one
INSERT INTO event_recovery_jobs(account_id,app_id,selection,rate_per_second,window_started_at,created_at,updated_at,next_attempt_at,expires_at,request_id)
VALUES (sqlc.arg(account_id)::uuid,sqlc.arg(app_id)::uuid,sqlc.arg(selection)::jsonb,sqlc.arg(rate_per_second)::integer,sqlc.arg(now_at)::timestamptz,sqlc.arg(now_at)::timestamptz,sqlc.arg(now_at)::timestamptz,sqlc.arg(now_at)::timestamptz,sqlc.arg(expires_at)::timestamptz,sqlc.narg(request_id)::uuid) RETURNING id;

-- name: EventRecoveryInsertItem :exec
INSERT INTO event_recovery_items(job_id,position,outbox_id,subscription_id,event_source,event_id,event_type,failed_at,failure_code,retryable,expected_progress)
VALUES (sqlc.arg(job_id)::uuid,sqlc.arg(position)::bigint,sqlc.arg(outbox_id)::bigint,sqlc.arg(subscription_id)::text,sqlc.arg(event_source)::text,sqlc.arg(event_id)::text,sqlc.arg(event_type)::text,sqlc.arg(failed_at)::timestamptz,sqlc.arg(failure_code)::text,sqlc.arg(retryable)::boolean,sqlc.arg(expected_progress)::jsonb);

-- name: EventRecoveryGet :one
SELECT j.*,
 (SELECT count(*) FROM event_recovery_items i WHERE i.job_id=j.id)::bigint AS selected_count,
 (SELECT count(*) FROM event_recovery_items i WHERE i.job_id=j.id AND i.state='pending')::bigint AS pending_count,
 (SELECT count(*) FROM event_recovery_items i WHERE i.job_id=j.id AND i.state='queued')::bigint AS queued_count,
 (SELECT count(*) FROM event_recovery_items i WHERE i.job_id=j.id AND i.state='skipped')::bigint AS skipped_count,
 (SELECT count(*) FROM event_recovery_items i WHERE i.job_id=j.id AND i.state='cancelled')::bigint AS cancelled_count
FROM event_recovery_jobs j JOIN apps a ON a.id=j.app_id AND a.account_id=j.account_id
WHERE j.id=sqlc.arg(job_id)::uuid AND j.account_id=sqlc.arg(account_id)::uuid;

-- name: EventRecoveryLock :one
SELECT j.* FROM event_recovery_jobs j WHERE j.id=sqlc.arg(job_id)::uuid AND j.account_id=sqlc.arg(account_id)::uuid FOR UPDATE;

-- name: EventRecoveryNextJob :one
SELECT * FROM event_recovery_jobs WHERE state IN ('running','paused')
 AND (expires_at<=sqlc.arg(now_at)::timestamptz OR (state='running' AND next_attempt_at<=sqlc.arg(now_at)::timestamptz
 AND (window_started_at+interval '1 second'<=sqlc.arg(now_at)::timestamptz OR window_count<rate_per_second)))
ORDER BY CASE WHEN expires_at<=sqlc.arg(now_at)::timestamptz THEN expires_at ELSE next_attempt_at END,id LIMIT 1 FOR UPDATE SKIP LOCKED;

-- name: EventRecoveryNextItem :one
SELECT * FROM event_recovery_items WHERE job_id=sqlc.arg(job_id)::uuid AND state='pending' ORDER BY position LIMIT 1;

-- name: EventRecoverySetItem :exec
UPDATE event_recovery_items SET state=sqlc.arg(state)::text,reason=sqlc.arg(reason)::text WHERE job_id=sqlc.arg(job_id)::uuid AND position=sqlc.arg(position)::bigint AND state='pending';

-- name: EventRecoverySchedule :exec
UPDATE event_recovery_jobs SET updated_at=sqlc.arg(now_at)::timestamptz,next_attempt_at=sqlc.arg(next_at)::timestamptz,
 state=CASE WHEN EXISTS (SELECT 1 FROM event_recovery_items i WHERE i.job_id=event_recovery_jobs.id AND i.state='pending') THEN 'running' ELSE 'completed' END,
 completed_at=CASE WHEN EXISTS (SELECT 1 FROM event_recovery_items i WHERE i.job_id=event_recovery_jobs.id AND i.state='pending') THEN NULL ELSE sqlc.arg(now_at)::timestamptz END
WHERE id=sqlc.arg(job_id)::uuid;

-- name: EventRecoveryCancelItems :exec
UPDATE event_recovery_items SET state='cancelled',reason=sqlc.arg(reason)::text WHERE job_id=sqlc.arg(job_id)::uuid AND state='pending';

-- name: EventRecoveryCancelJob :exec
UPDATE event_recovery_jobs SET state='cancelled',paused_at=NULL,completed_at=sqlc.arg(now_at)::timestamptz,updated_at=sqlc.arg(now_at)::timestamptz WHERE id=sqlc.arg(job_id)::uuid AND state IN ('running','paused');

-- name: EventRecoveryItems :many
SELECT * FROM event_recovery_items WHERE job_id=sqlc.arg(job_id)::uuid AND position>sqlc.arg(after_position)::bigint ORDER BY position LIMIT sqlc.arg(page_limit)::integer;

-- name: EventRecoveryTarget :one
SELECT o.id,o.recipient_claims,o.state AS receipt_state,p.progress::jsonb AS progress,
 (coalesce(event_recovery_failure_identity(p.progress)=sqlc.arg(expected_progress)::jsonb,false) AND coalesce(routed.state,p.progress->>'state','pending')='failed' AND (NOT o.recipient_claims OR coalesce(routed.state,'')='failed'))::boolean AS unchanged,
 a.status<>'deleted' AS target_available
FROM event_fanout_outbox o
JOIN apps a ON a.id=sqlc.arg(app_id)::uuid AND a.account_id=o.account_id
LEFT JOIN event_fanout_recipients routed ON routed.outbox_id=o.id AND routed.subscription_id=sqlc.arg(subscription_id)::text
CROSS JOIN LATERAL (SELECT o.recipient_progress->sqlc.arg(subscription_id)::text AS progress) p
WHERE o.id=sqlc.arg(outbox_id)::bigint AND o.account_id=sqlc.arg(account_id)::uuid
 AND EXISTS (SELECT 1 FROM jsonb_array_elements(coalesce(o.recipient_snapshot,'[]'::jsonb)) r WHERE r->>'id'=sqlc.arg(subscription_id)::text AND r->>'app_id'=a.id::text)
FOR UPDATE OF o;

-- name: EventRecoveryPrune :execrows
DELETE FROM event_recovery_jobs WHERE id IN (
 SELECT id FROM event_recovery_jobs WHERE state IN ('completed','cancelled') AND completed_at<sqlc.arg(before_at)::timestamptz
 ORDER BY completed_at,id LIMIT sqlc.arg(page_limit)::integer FOR UPDATE SKIP LOCKED
);

-- name: EventRecoveryApp :one
SELECT id FROM apps WHERE id=sqlc.arg(app_id)::uuid AND account_id=sqlc.arg(account_id)::uuid AND status<>'deleted' FOR SHARE;

-- name: EventRecoveryUsePermit :exec
UPDATE event_recovery_jobs SET
 window_started_at=CASE WHEN window_started_at+interval '1 second'<=sqlc.arg(now_at)::timestamptz THEN sqlc.arg(now_at)::timestamptz ELSE window_started_at END,
 window_count=CASE WHEN window_started_at+interval '1 second'<=sqlc.arg(now_at)::timestamptz THEN 1 ELSE window_count+1 END,
 next_attempt_at=CASE
 WHEN window_started_at+interval '1 second'<=sqlc.arg(now_at)::timestamptz THEN
  CASE WHEN rate_per_second=1 THEN sqlc.arg(now_at)::timestamptz+interval '1 second' ELSE sqlc.arg(now_at)::timestamptz END
 WHEN window_count+1>=rate_per_second THEN window_started_at+interval '1 second'
 ELSE sqlc.arg(now_at)::timestamptz END
WHERE id=sqlc.arg(job_id)::uuid;

-- name: EventRecoveryRecordReplay :exec
UPDATE event_recovery_items SET state='queued',reason='',
 replay_invocation_id=sqlc.arg(replay_invocation_id)::uuid,
 replay_generation=sqlc.arg(replay_generation)::bigint,
 replay_created_at=sqlc.arg(replay_created_at)::timestamptz
WHERE job_id=sqlc.arg(job_id)::uuid AND position=sqlc.arg(position)::bigint AND state='pending';

-- name: EventRecoveryExecutionObservations :many
-- Exact replay identity and generation, never the latest descendant's outcome.
SELECT item.position,
 coalesce(result.state,'')::text AS result_state,coalesce(result.attempts,0)::integer AS result_attempts,
 result.completed_at AS result_completed_at,result.recorded_at AS result_recorded_at,
 coalesce(result.evidence_source,'')::text AS result_evidence_source,
 coalesce(inv.state,'')::text AS invocation_state,
 coalesce(inv.attempts,0)::integer AS invocation_attempts,
 coalesce(inv.outcome,'')::text AS invocation_outcome,
 inv.completed_at AS invocation_completed_at,
 coalesce(h.outcome,'')::text AS attempt_outcome,
 coalesce(h.attempt,0)::integer AS attempt_number,
 h.finished_at AS attempt_finished_at
FROM event_recovery_items item
JOIN event_recovery_jobs job ON job.id=item.job_id
LEFT JOIN event_recovery_execution_results result ON result.job_id=item.job_id AND result.position=item.position
 AND result.replay_invocation_id=item.replay_invocation_id AND result.replay_generation=item.replay_generation
 AND result.replay_created_at=item.replay_created_at AND result.recorded_at<=sqlc.arg(now_at)::timestamptz
LEFT JOIN invocations inv ON inv.id=item.replay_invocation_id AND inv.account_id=job.account_id AND inv.app_id=job.app_id
 AND inv.replay_generation=item.replay_generation AND inv.created_at=item.replay_created_at AND result.job_id IS NULL
LEFT JOIN LATERAL (
 SELECT history.outcome,history.attempt,history.finished_at
 FROM invocation_attempt_history history
 WHERE history.invocation_id=item.replay_invocation_id AND history.replay_generation=item.replay_generation
  AND history.account_id=job.account_id AND history.app_id=job.app_id
  AND history.started_at>=item.replay_created_at AND history.started_at<=sqlc.arg(now_at)::timestamptz
  AND history.retain_until>sqlc.arg(now_at)::timestamptz
  AND EXISTS (SELECT 1 FROM invocations owner WHERE owner.id=item.replay_invocation_id AND owner.account_id=job.account_id
   AND owner.app_id=job.app_id AND owner.created_at=item.replay_created_at)
 ORDER BY history.attempt DESC LIMIT 1
) h ON inv.id IS NULL AND result.job_id IS NULL
WHERE job.id=sqlc.arg(job_id)::uuid AND job.account_id=sqlc.arg(account_id)::uuid
 AND job.selection->>'mode'='execution' AND item.state='queued'
 AND item.position>sqlc.arg(after_position)::bigint AND item.position<=sqlc.arg(through_position)::bigint
ORDER BY item.position;


-- name: EventRecoveryPause :exec
UPDATE event_recovery_jobs SET state='paused',paused_at=sqlc.arg(now_at)::timestamptz,updated_at=sqlc.arg(now_at)::timestamptz
WHERE id=sqlc.arg(job_id)::uuid AND state='running';

-- name: EventRecoveryResume :exec
UPDATE event_recovery_jobs SET state='running',paused_at=NULL,wait_reason='',capacity_scope='',capacity_wait_started_at=NULL,capacity_wait_observed_at=NULL,updated_at=sqlc.arg(now_at)::timestamptz,
 next_attempt_at=greatest(next_attempt_at,sqlc.arg(now_at)::timestamptz,
 CASE WHEN window_started_at+interval '1 second'>sqlc.arg(now_at)::timestamptz AND window_count>=rate_per_second THEN window_started_at+interval '1 second' ELSE sqlc.arg(now_at)::timestamptz END)
WHERE id=sqlc.arg(job_id)::uuid AND state='paused';

-- name: EventRecoverySetRate :exec
UPDATE event_recovery_jobs SET rate_per_second=sqlc.arg(rate_per_second)::integer,updated_at=sqlc.arg(now_at)::timestamptz,
 next_attempt_at=greatest(next_attempt_at,
 CASE WHEN window_started_at+interval '1 second'>sqlc.arg(now_at)::timestamptz AND window_count>=sqlc.arg(rate_per_second)::integer THEN window_started_at+interval '1 second' ELSE sqlc.arg(now_at)::timestamptz END)
WHERE id=sqlc.arg(job_id)::uuid AND state IN ('running','paused') AND rate_per_second<>sqlc.arg(rate_per_second)::integer;

-- name: EventRecoveryList :many
WITH page AS MATERIALIZED (
 SELECT j.* FROM event_recovery_jobs j
 WHERE j.account_id=sqlc.arg(account_id)::uuid AND j.app_id=sqlc.arg(app_id)::uuid
 AND (sqlc.arg(filter_state)::text='' OR j.state=sqlc.arg(filter_state)::text)
 AND (sqlc.arg(mode)::text='' OR coalesce(nullif(j.selection->>'mode',''),'routing')=sqlc.arg(mode)::text)
 AND (sqlc.arg(subscription_id)::text='' OR j.selection->>'subscription_id'=sqlc.arg(subscription_id)::text OR EXISTS (SELECT 1 FROM event_recovery_items i WHERE i.job_id=j.id AND i.subscription_id=sqlc.arg(subscription_id)::text))
 AND (sqlc.narg(created_after)::timestamptz IS NULL OR j.created_at>sqlc.narg(created_after)::timestamptz)
 AND (sqlc.narg(created_before)::timestamptz IS NULL OR j.created_at<sqlc.narg(created_before)::timestamptz)
 AND (NOT sqlc.arg(has_cursor)::boolean OR (j.created_at,j.id)<(sqlc.arg(cursor_created)::timestamptz,sqlc.arg(cursor_id)::uuid))
ORDER BY j.created_at DESC,j.id DESC LIMIT sqlc.arg(page_limit)::integer
)
SELECT j.*,
 (SELECT count(*) FROM event_recovery_items i WHERE i.job_id=j.id)::bigint AS selected_count,
 (SELECT count(*) FROM event_recovery_items i WHERE i.job_id=j.id AND i.state='pending')::bigint AS pending_count,
 (SELECT count(*) FROM event_recovery_items i WHERE i.job_id=j.id AND i.state='queued')::bigint AS queued_count,
 (SELECT count(*) FROM event_recovery_items i WHERE i.job_id=j.id AND i.state='skipped')::bigint AS skipped_count,
 (SELECT count(*) FROM event_recovery_items i WHERE i.job_id=j.id AND i.state='cancelled')::bigint AS cancelled_count
FROM page j ORDER BY j.created_at DESC,j.id DESC;

-- name: EventRecoveryListApp :one
SELECT id FROM apps WHERE id=sqlc.arg(app_id)::uuid AND account_id=sqlc.arg(account_id)::uuid AND status<>'deleted';

-- name: EventRecoveryHistoryInsert :exec
INSERT INTO event_recovery_history(job_id,occurred_at,action,actor_kind,actor_id,reason,previous_state,state,previous_rate,rate)
SELECT id,sqlc.arg(now_at)::timestamptz,sqlc.arg(action)::text,sqlc.arg(actor_kind)::text,sqlc.arg(actor_id)::text,sqlc.arg(reason)::text,sqlc.arg(previous_state)::text,state,sqlc.arg(previous_rate)::integer,rate_per_second
FROM event_recovery_jobs WHERE id=sqlc.arg(job_id)::uuid;

-- name: EventRecoveryHistoryCancel :exec
INSERT INTO event_recovery_history(job_id,occurred_at,action,actor_kind,actor_id,reason,previous_state,state,previous_rate,rate)
SELECT id,sqlc.arg(now_at)::timestamptz,sqlc.arg(action)::text,sqlc.arg(actor_kind)::text,sqlc.arg(actor_id)::text,sqlc.arg(reason)::text,state,'cancelled',rate_per_second,rate_per_second
FROM event_recovery_jobs WHERE id=sqlc.arg(job_id)::uuid AND state IN ('running','paused');

-- name: EventRecoveryHistoryList :many
SELECT h.* FROM event_recovery_history h JOIN event_recovery_jobs j ON j.id=h.job_id
WHERE j.id=sqlc.arg(job_id)::uuid AND j.account_id=sqlc.arg(account_id)::uuid AND h.id>sqlc.arg(after_id)::bigint
ORDER BY h.id LIMIT sqlc.arg(page_limit)::integer;

-- name: EventRecoveryProgress :exec
UPDATE event_recovery_jobs SET last_progress_at=sqlc.arg(now_at)::timestamptz,wait_reason='',capacity_scope='',capacity_wait_started_at=NULL,capacity_wait_observed_at=NULL WHERE id=sqlc.arg(job_id)::uuid;

-- name: EventRecoveryWait :exec
UPDATE event_recovery_jobs SET
 capacity_wait_started_at=CASE WHEN sqlc.arg(wait_reason)::text='capacity' THEN CASE WHEN wait_reason='capacity' AND capacity_wait_started_at IS NOT NULL THEN capacity_wait_started_at ELSE sqlc.arg(now_at)::timestamptz END ELSE NULL END,
 capacity_wait_observed_at=CASE WHEN sqlc.arg(wait_reason)::text='capacity' THEN sqlc.arg(now_at)::timestamptz ELSE NULL END,
 capacity_scope=CASE WHEN sqlc.arg(wait_reason)::text='capacity' THEN sqlc.arg(capacity_scope)::text ELSE '' END,
 wait_reason=sqlc.arg(wait_reason)::text WHERE id=sqlc.arg(job_id)::uuid;

-- name: EventRecoveryHealth :many
SELECT j.*, (SELECT count(*) FROM event_recovery_items i WHERE i.job_id=j.id AND i.state='pending')::bigint AS pending_count
FROM event_recovery_jobs j WHERE j.account_id=sqlc.arg(account_id)::uuid AND j.app_id=sqlc.arg(app_id)::uuid AND j.state IN ('running','paused')
ORDER BY j.created_at,j.id;

-- name: EventRecoveryNotificationJob :one
SELECT account_id FROM event_recovery_jobs WHERE id=sqlc.arg(job_id)::uuid;

-- name: EventRecoveryEnqueueNotification :exec
WITH recipients AS (
 SELECT coalesce(array_agg(h.id ORDER BY h.id),'{}'::uuid[]) AS ids FROM app_webhooks h
 JOIN event_recovery_jobs j ON j.id=sqlc.arg(job_id)::uuid AND h.account_id=j.account_id AND h.app_id=j.app_id
 WHERE h.scope='app' AND h.enabled AND (cardinality(h.event_filter)=0 OR sqlc.arg(event)::text=ANY(h.event_filter))
), captured AS (
 UPDATE event_recovery_jobs j SET notification_receipts=j.notification_receipts||jsonb_build_object(sqlc.arg(event)::text,
 jsonb_build_object('event_id',sqlc.arg(event_id)::uuid,'captured_at',CASE WHEN sqlc.arg(event)::text='event_recovery.execution_finished' THEN j.execution_finished_at ELSE j.completed_at END,'recipient_webhook_ids',to_jsonb(r.ids)))
 FROM recipients r WHERE j.id=sqlc.arg(job_id)::uuid AND j.state IN ('completed','cancelled')
 AND NOT j.notification_receipts ? sqlc.arg(event)::text
 RETURNING j.id,j.account_id,j.app_id,j.completed_at,j.execution_finished_at,r.ids
)
INSERT INTO app_webhook_event_outbox(id,account_id,app_id,event,source_id,payload,recipient_webhook_ids,created_at)
SELECT sqlc.arg(event_id)::uuid,j.account_id,j.app_id,sqlc.arg(event)::text,j.id,sqlc.arg(payload)::jsonb,j.ids,CASE WHEN sqlc.arg(event)::text='event_recovery.execution_finished' THEN j.execution_finished_at ELSE j.completed_at END
FROM captured j WHERE cardinality(j.ids)>0
ON CONFLICT (event,source_id) DO NOTHING;

-- name: EventRecoveryScheduleTerminalState :one
UPDATE event_recovery_jobs SET updated_at=sqlc.arg(now_at)::timestamptz,next_attempt_at=sqlc.arg(next_at)::timestamptz,
 state=CASE WHEN EXISTS (SELECT 1 FROM event_recovery_items i WHERE i.job_id=event_recovery_jobs.id AND i.state='pending') THEN 'running' ELSE 'completed' END,
 completed_at=CASE WHEN EXISTS (SELECT 1 FROM event_recovery_items i WHERE i.job_id=event_recovery_jobs.id AND i.state='pending') THEN NULL ELSE sqlc.arg(now_at)::timestamptz END
WHERE id=sqlc.arg(job_id)::uuid RETURNING state;

-- name: EventRecoveryPreflight :many
WITH slots AS MATERIALIZED (
 SELECT s.* FROM event_delivery_slots s JOIN invocations v ON v.id=s.invocation_id
 WHERE s.account_id=sqlc.arg(account_id)::uuid AND v.state IN ('pending','dispatching')
), totals AS (
 SELECT count(*)::bigint AS account_count,count(*) FILTER (WHERE slots.app_id=j.app_id)::bigint AS app_count
 FROM slots CROSS JOIN event_recovery_jobs j WHERE j.id=sqlc.arg(job_id)::uuid
), consumers AS (
 SELECT app_id,subscription_id,count(*)::bigint AS consumer_count FROM slots GROUP BY app_id,subscription_id
)
SELECT item.position,acct.plan,
 (o.delivered_at + sqlc.arg(retention_seconds)::bigint * interval '1 second')::timestamptz AS receipt_retain_until,
 coalesce(event_receipt_retention_hold(o.account_id,o.id,o.created_at,sqlc.arg(job_cutoff_at)::timestamptz,sqlc.arg(now_at)::timestamptz)<>'',false)::boolean AS receipt_retention_held,
 CASE WHEN app.status='deleted' THEN 'target_unavailable'
 WHEN j.selection->>'mode'='execution' THEN CASE
  WHEN inv.id IS NULL OR inv.state<>item.expected_progress->>'state' OR inv.attempts<>(item.expected_progress->>'attempts')::integer
   OR inv.replay_generation<>(item.expected_progress->>'generation')::bigint OR inv.created_at<>(item.expected_progress->>'created_at')::timestamptz
   OR inv.completed_at IS DISTINCT FROM (item.expected_progress->>'completed_at')::timestamptz
   OR (coalesce(item.expected_progress->>'parent_job_id','')<>'' AND inv.outcome='uncertain')
   OR EXISTS (SELECT 1 FROM invocation_plain_replays p WHERE p.parent_invocation_id=inv.id)
   OR EXISTS (SELECT 1 FROM invocation_keyed_replays k WHERE k.parent_invocation_id=inv.id) THEN 'changed'
  WHEN inv.work_expires_at<=sqlc.arg(now_at)::timestamptz OR inv.start_deadline_at<=sqlc.arg(now_at)::timestamptz THEN 'expired'
  WHEN o.id IS NULL OR NOT (EXISTS (SELECT 1 FROM jsonb_array_elements(coalesce(o.recipient_snapshot,'[]'::jsonb)) r WHERE r->>'id'=item.subscription_id AND r->>'app_id'=j.app_id::text)
   OR EXISTS (SELECT 1 FROM event_fanout_recipients r WHERE r.outbox_id=o.id AND r.subscription_id=item.subscription_id AND r.recipient->>'app_id'=j.app_id::text)) THEN 'receipt_expired'
  WHEN inv.state='dead_letter' AND NOT EXISTS (SELECT 1 FROM production_dead_letter_events e WHERE e.id=nullif(item.expected_progress->>'dead_letter_id','')::uuid AND e.account_id=j.account_id AND e.app_id=j.app_id AND e.source='invocation' AND e.source_id=inv.id AND e.replayed_at IS NULL) THEN 'changed'
  WHEN inv.state='failed' AND (inv.queue_binding_id IS NOT NULL OR coalesce(inv.queue_name,'')<>'' OR inv.environment_id IS NOT NULL
   OR EXISTS (SELECT 1 FROM customer_operation_executions op WHERE op.invocation_id=inv.id)
   OR (inv.work_policy_name IS NOT NULL AND (coalesce(octet_length(inv.work_key_digest),0)<>32 OR coalesce(inv.work_sequence,0)<=0))) THEN 'changed'
  WHEN inv.state='failed' AND inv.platform_tenant_id IS NOT NULL AND tenant.id IS NULL THEN 'unknown'
  WHEN inv.state='failed' AND tenant.status='suspended' THEN 'target_unavailable'
  ELSE 'eligible' END
 ELSE CASE
  WHEN o.id IS NULL OR recipient.value IS NULL THEN 'receipt_expired'
  WHEN NOT coalesce(event_recovery_failure_identity(o.recipient_progress->item.subscription_id)=item.expected_progress,false)
   OR coalesce(routed.state,o.recipient_progress->item.subscription_id->>'state','pending')<>'failed'
   OR (o.recipient_claims AND coalesce(routed.state,'')<>'failed') THEN 'changed'
  WHEN NOT o.recipient_claims AND o.state='processing' THEN 'legacy_claim'
  WHEN event_recipient_delivery_deadline(recipient.value,o.created_at,'{}'::jsonb)<=sqlc.arg(now_at)::timestamptz THEN 'expired'
  ELSE 'eligible' END END::text AS reason,
 (slot.invocation_id IS NOT NULL AND j.selection->>'mode'='execution')::boolean AS capacity_tracked,
 totals.account_count,totals.app_count,coalesce(c.consumer_count,0)::bigint AS consumer_count
FROM event_recovery_items item JOIN event_recovery_jobs j ON j.id=item.job_id
JOIN apps app ON app.id=j.app_id AND app.account_id=j.account_id JOIN accounts acct ON acct.id=j.account_id
LEFT JOIN event_fanout_outbox o ON o.id=item.outbox_id AND o.account_id=j.account_id
LEFT JOIN LATERAL (SELECT r AS value FROM jsonb_array_elements(coalesce(o.recipient_snapshot,'[]'::jsonb)) r WHERE r->>'id'=item.subscription_id AND r->>'app_id'=j.app_id::text LIMIT 1) recipient ON true
LEFT JOIN event_fanout_recipients routed ON routed.outbox_id=o.id AND routed.subscription_id=item.subscription_id
LEFT JOIN invocations inv ON inv.id=(item.expected_progress->>'invocation_id')::uuid AND inv.account_id=j.account_id AND inv.app_id=j.app_id
LEFT JOIN platform_tenants tenant ON tenant.id=inv.platform_tenant_id AND tenant.account_id=j.account_id
LEFT JOIN event_delivery_slots slot ON slot.invocation_id=inv.id
LEFT JOIN consumers c ON c.app_id=slot.app_id AND c.subscription_id=slot.subscription_id
CROSS JOIN totals
WHERE j.id=sqlc.arg(job_id)::uuid AND j.account_id=sqlc.arg(account_id)::uuid AND item.state='pending'
ORDER BY item.position LIMIT sqlc.arg(page_limit)::integer;

-- name: EventRecoveryPreflightJob :one
SELECT * FROM event_recovery_jobs WHERE id=sqlc.arg(job_id)::uuid AND account_id=sqlc.arg(account_id)::uuid;

-- name: EventRecoveryClaimExecutionNotification :one
SELECT j.id,j.account_id FROM event_recovery_jobs j
WHERE NOT j.execution_notification_captured AND j.selection->>'mode'='execution'
 AND j.state IN ('completed','cancelled') AND j.completed_at<=sqlc.arg(now_at)::timestamptz
 AND j.execution_notification_next_at<=sqlc.arg(now_at)::timestamptz
ORDER BY j.execution_notification_next_at,j.id LIMIT 1 FOR UPDATE OF j SKIP LOCKED;

-- name: EventRecoveryCaptureExecutionNotification :exec
UPDATE event_recovery_jobs SET execution_notification_captured=true,execution_finished_at=sqlc.narg(finished_at)::timestamptz
WHERE id=sqlc.arg(job_id)::uuid AND NOT execution_notification_captured;

-- name: EventRecoveryDeferExecutionNotification :exec
UPDATE event_recovery_jobs SET execution_notification_next_at=sqlc.arg(next_at)::timestamptz
WHERE id=sqlc.arg(job_id)::uuid AND NOT execution_notification_captured;

-- name: EventRecoveryRetryExisting :one
SELECT id FROM event_recovery_jobs
WHERE account_id=sqlc.arg(account_id)::uuid AND request_id=sqlc.arg(request_id)::uuid
FOR KEY SHARE;

-- name: EventRecoveryRetryCandidates :many
SELECT item.outbox_id,item.event_source,item.event_id,item.event_type,item.subscription_id,
 coalesce(result.completed_at,result.recorded_at)::timestamptz AS failed_at,
 (CASE result.state WHEN 'dead_lettered' THEN 'dead_letter' ELSE result.state END)::text AS failure_code,
 true::boolean AS retryable,
 jsonb_build_object('invocation_id',result.replay_invocation_id::text,
 'state',CASE result.state WHEN 'dead_lettered' THEN 'dead_letter' ELSE result.state END,
 'attempts',result.attempts,'generation',result.replay_generation,'created_at',result.replay_created_at,
 'completed_at',result.completed_at,'dead_letter_id',coalesce(dead.id::text,''),
 'parent_job_id',item.job_id::text,'parent_position',item.position)::jsonb AS expected_progress
FROM event_recovery_items item JOIN event_recovery_jobs parent ON parent.id=item.job_id
JOIN event_recovery_execution_results result ON result.job_id=item.job_id AND result.position=item.position
 AND result.replay_invocation_id=item.replay_invocation_id AND result.replay_generation=item.replay_generation
 AND result.replay_created_at=item.replay_created_at
LEFT JOIN LATERAL (
 SELECT dead.id FROM production_dead_letter_events dead JOIN invocations inv ON inv.id=dead.source_id
 WHERE dead.account_id=parent.account_id AND dead.app_id=parent.app_id AND dead.source='invocation'
  AND dead.source_id=result.replay_invocation_id AND dead.replayed_at IS NULL
  AND inv.account_id=parent.account_id AND inv.app_id=parent.app_id
  AND inv.replay_generation=result.replay_generation AND inv.created_at=result.replay_created_at
 ORDER BY dead.id LIMIT 1
) dead ON true
WHERE parent.id=sqlc.arg(parent_job_id)::uuid AND parent.account_id=sqlc.arg(account_id)::uuid AND parent.app_id=sqlc.arg(app_id)::uuid
 AND parent.selection->>'mode'='execution' AND parent.state IN ('completed','cancelled') AND item.state='queued'
 AND result.state IN ('failed','dead_lettered') AND result.recorded_at<=sqlc.arg(now_at)::timestamptz
 AND (sqlc.arg(outcome)::text='' OR CASE result.state WHEN 'dead_lettered' THEN 'dead_letter' ELSE result.state END=sqlc.arg(outcome)::text)
 AND (sqlc.arg(subscription_id)::text='' OR item.subscription_id=sqlc.arg(subscription_id)::text)
 AND (sqlc.arg(event_source)::text='' OR item.event_source=sqlc.arg(event_source)::text)
 AND (sqlc.arg(event_type)::text='' OR item.event_type=sqlc.arg(event_type)::text)
 AND coalesce(result.completed_at,result.recorded_at)<=sqlc.arg(failed_before)::timestamptz
ORDER BY item.position LIMIT sqlc.arg(page_limit)::integer;

-- name: EventRecoveryExecutionHealthJobs :many
SELECT j.id, j.completed_at, j.selection, j.state, j.execution_notification_captured,
 (SELECT count(*) FROM event_recovery_items i WHERE i.job_id=j.id AND i.state='queued') AS queued_count
FROM event_recovery_jobs j
WHERE j.account_id=sqlc.arg(account_id) AND j.app_id=sqlc.arg(app_id)
 AND j.state IN ('completed','cancelled') AND j.selection->>'mode'='execution'
 AND j.completed_at<=sqlc.arg(now_at) AND j.execution_finished_at IS NULL
 AND EXISTS (
 SELECT 1 FROM event_recovery_items i
 LEFT JOIN event_recovery_execution_results r ON r.job_id=i.job_id AND r.position=i.position
 AND r.replay_invocation_id=i.replay_invocation_id AND r.replay_generation=i.replay_generation
 AND r.replay_created_at=i.replay_created_at AND r.recorded_at<=sqlc.arg(now_at)
 WHERE i.job_id=j.id AND i.state='queued' AND r.job_id IS NULL)
ORDER BY j.completed_at,j.id LIMIT sqlc.arg(job_limit);

-- name: EventRecoveryNotificationEvidence :one
SELECT j.notification_receipts,j.execution_notification_captured,a.slug AS app_slug
FROM event_recovery_jobs j JOIN apps a ON a.id=j.app_id AND a.account_id=j.account_id
WHERE j.id=sqlc.arg(job_id)::uuid AND j.account_id=sqlc.arg(account_id)::uuid;

-- name: EventRecoveryNotificationOutbox :many
SELECT id,event,created_at,recipient_webhook_ids FROM app_webhook_event_outbox
WHERE source_id=sqlc.arg(job_id)::uuid AND account_id=sqlc.arg(account_id)::uuid AND app_id=sqlc.arg(app_id)::uuid
 AND event IN ('event_recovery.completed','event_recovery.cancelled','event_recovery.expired','event_recovery.execution_finished');

-- name: EventRecoveryNotificationDeliveries :many
SELECT d.webhook_id,d.id,d.status,d.attempt,d.replay_generation,coalesce(d.last_response_code,0)::integer AS last_response_code,
 d.next_attempt_at,d.delivered_at,(h.id IS NOT NULL)::boolean AS receiver_available
FROM app_webhook_deliveries d
LEFT JOIN app_webhooks h ON h.id=d.webhook_id AND h.account_id=d.account_id AND h.app_id=d.app_id AND h.scope='app'
WHERE d.source_event_id=sqlc.arg(event_id)::uuid AND d.event=sqlc.arg(event)::text
 AND d.account_id=sqlc.arg(account_id)::uuid AND d.app_id=sqlc.arg(app_id)::uuid
ORDER BY d.webhook_id,d.id LIMIT sqlc.arg(receiver_limit)::integer;

-- name: EventRecoveryNotificationReceivers :many
SELECT id FROM app_webhooks WHERE account_id=sqlc.arg(account_id)::uuid AND app_id=sqlc.arg(app_id)::uuid
 AND scope='app' AND id=ANY(sqlc.arg(webhook_ids)::uuid[]);

-- name: EventRecoveryNotificationHealthJobs :many
SELECT j.id FROM event_recovery_jobs j
WHERE j.account_id=sqlc.arg(account_id)::uuid AND j.app_id=sqlc.arg(app_id)::uuid
 AND j.state IN ('completed','cancelled') AND j.completed_at<=sqlc.arg(now_at)::timestamptz
 AND (
  NOT j.notification_receipts ?| ARRAY['event_recovery.completed','event_recovery.cancelled','event_recovery.expired']::text[]
  OR (SELECT count(*) FROM jsonb_object_keys(j.notification_receipts) key WHERE key IN ('event_recovery.completed','event_recovery.cancelled','event_recovery.expired'))>1
  OR (j.execution_finished_at IS NOT NULL AND NOT j.notification_receipts ? 'event_recovery.execution_finished')
  OR EXISTS (
   SELECT 1 FROM jsonb_each(j.notification_receipts) receipt
   WHERE receipt.value->>'captured_at' IS NULL OR receipt.value->>'event_id' IS NULL
    OR coalesce(jsonb_array_length(receipt.value->'recipient_webhook_ids'),0)=0
    OR jsonb_array_length(receipt.value->'recipient_webhook_ids')>sqlc.arg(receiver_limit)::integer
    OR EXISTS (
     SELECT 1 FROM jsonb_array_elements_text(receipt.value->'recipient_webhook_ids') selected(webhook_id)
     LEFT JOIN app_webhook_deliveries d ON d.source_event_id=(receipt.value->>'event_id')::uuid
      AND d.webhook_id=selected.webhook_id::uuid AND d.event=receipt.key AND d.account_id=j.account_id AND d.app_id=j.app_id
     WHERE d.id IS NULL OR d.status<>'succeeded'
    )
  )
 )
ORDER BY j.completed_at,j.id LIMIT sqlc.arg(job_limit)::integer;

-- name: EventRecoveryNotificationRetryOwner :one
SELECT app_id,notification_retry_receipts FROM event_recovery_jobs
WHERE id=sqlc.arg(job_id)::uuid AND account_id=sqlc.arg(account_id)::uuid FOR UPDATE;

-- name: EventRecoveryNotificationRetrySave :exec
UPDATE event_recovery_jobs SET notification_retry_receipts=notification_retry_receipts || jsonb_build_object(sqlc.arg(request_id)::text,sqlc.arg(receipt)::jsonb)
WHERE id=sqlc.arg(job_id)::uuid AND account_id=sqlc.arg(account_id)::uuid;

-- name: EventRecoveryNotificationRetryPlan :one
SELECT plan FROM accounts WHERE id=sqlc.arg(account_id)::uuid;

-- name: EventRecoveryNotificationRetryPlanLock :one
SELECT plan FROM accounts WHERE id=sqlc.arg(account_id)::uuid FOR SHARE;

-- name: EventRecoveryNotificationRetryHooks :many
SELECT id,enabled FROM app_webhooks WHERE account_id=sqlc.arg(account_id)::uuid AND app_id=sqlc.arg(app_id)::uuid AND scope='app' AND id=ANY(sqlc.arg(webhook_ids)::uuid[]);

-- name: EventRecoveryNotificationRetryHookLock :one
SELECT enabled FROM app_webhooks WHERE id=sqlc.arg(webhook_id)::uuid AND account_id=sqlc.arg(account_id)::uuid AND app_id=sqlc.arg(app_id)::uuid AND scope='app' FOR SHARE;

-- name: EventRecoveryNotificationRetryDeliveryLock :one
SELECT status,replay_generation FROM app_webhook_deliveries
WHERE id=sqlc.arg(delivery_id)::uuid AND webhook_id=sqlc.arg(webhook_id)::uuid AND account_id=sqlc.arg(account_id)::uuid AND app_id=sqlc.arg(app_id)::uuid AND event=sqlc.arg(event)::text AND source_event_id=sqlc.arg(event_id)::uuid FOR UPDATE;

-- name: EventRecoveryNotificationRetryReset :execrows
UPDATE app_webhook_deliveries SET status='pending',attempt=0,replay_generation=replay_generation+1,last_error='',last_response_code=0,next_attempt_at=sqlc.arg(now_at)::timestamptz,updated_at=sqlc.arg(now_at)::timestamptz
WHERE id=sqlc.arg(delivery_id)::uuid AND account_id=sqlc.arg(account_id)::uuid AND webhook_id=sqlc.arg(webhook_id)::uuid AND app_id=sqlc.arg(app_id)::uuid AND event=sqlc.arg(event)::text AND source_event_id=sqlc.arg(event_id)::uuid AND status='dead' AND replay_generation=sqlc.arg(expected_generation)::integer;

-- name: EventRecoveryNotificationRetryHistoryOwner :one
SELECT app_id,notification_retry_receipts FROM event_recovery_jobs
WHERE id=sqlc.arg(job_id)::uuid AND account_id=sqlc.arg(account_id)::uuid;

-- name: EventRecoveryNotificationRetryGenerationOutcome :one
SELECT count(*)::integer AS retained_count,
       coalesce(max(a.attempt_number),0)::integer AS highest_attempt,
       coalesce(max(a.outcome) FILTER (WHERE a.outcome IN ('succeeded','dead')),'')::text AS terminal_outcome,
       (max(a.finished_at) FILTER (WHERE a.outcome IN ('succeeded','dead')))::timestamptz AS completed_at
FROM app_webhook_delivery_attempts a
JOIN app_webhook_deliveries d ON d.id=a.delivery_id
WHERE d.id=sqlc.arg(delivery_id)::uuid AND d.webhook_id=sqlc.arg(webhook_id)::uuid
 AND d.account_id=sqlc.arg(account_id)::uuid AND d.app_id=sqlc.arg(app_id)::uuid
 AND d.event=sqlc.arg(event)::text AND d.source_event_id=sqlc.arg(event_id)::uuid
 AND a.replay_generation=sqlc.arg(generation)::integer;

-- name: EventRecoveryNotificationRetryHistoryOutcomes :many
WITH targets AS (
 SELECT (value->>'delivery_id')::uuid AS delivery_id,
        (value->>'webhook_id')::uuid AS webhook_id,
        (value->>'event_id')::uuid AS event_id,
        value->>'event' AS event,
        (value->>'generation')::integer AS generation
 FROM jsonb_array_elements(sqlc.arg(targets)::jsonb)
)
SELECT t.delivery_id,t.generation,
       count(a.id)::integer AS retained_count,
       coalesce(max(a.attempt_number),0)::integer AS highest_attempt,
       coalesce(max(a.outcome) FILTER (WHERE a.outcome IN ('succeeded','dead')),'')::text AS terminal_outcome,
       (max(a.finished_at) FILTER (WHERE a.outcome IN ('succeeded','dead')))::timestamptz AS completed_at
FROM targets t
JOIN app_webhook_deliveries d ON d.id=t.delivery_id AND d.webhook_id=t.webhook_id
 AND d.source_event_id=t.event_id AND d.event=t.event
 AND d.account_id=sqlc.arg(account_id)::uuid AND d.app_id=sqlc.arg(app_id)::uuid
LEFT JOIN app_webhook_delivery_attempts a ON a.delivery_id=d.id AND a.replay_generation=t.generation
GROUP BY t.delivery_id,t.generation;
