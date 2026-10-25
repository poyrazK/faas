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
VALUES (sqlc.arg(job_id)::uuid,sqlc.arg(position)::bigint,nullif(sqlc.arg(outbox_id)::bigint,0),sqlc.arg(subscription_id)::text,sqlc.arg(event_source)::text,sqlc.arg(event_id)::text,sqlc.arg(event_type)::text,sqlc.arg(failed_at)::timestamptz,sqlc.arg(failure_code)::text,sqlc.arg(retryable)::boolean,sqlc.arg(expected_progress)::jsonb);

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
ORDER BY CASE WHEN expires_at<=sqlc.arg(now_at)::timestamptz THEN expires_at ELSE next_attempt_at END,id LIMIT 1 FOR NO KEY UPDATE SKIP LOCKED;

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
 WHERE s.account_id=sqlc.arg(account_id)::uuid AND s.live AND v.state IN ('pending','dispatching')
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
 totals.account_count,totals.app_count,coalesce(c.consumer_count,0)::bigint AS consumer_count,
 coalesce(capacity_control.max_pending_deliveries,0)::integer AS max_pending_deliveries
FROM event_recovery_items item JOIN event_recovery_jobs j ON j.id=item.job_id
JOIN apps app ON app.id=j.app_id AND app.account_id=j.account_id JOIN accounts acct ON acct.id=j.account_id
LEFT JOIN event_fanout_outbox o ON o.id=item.outbox_id AND o.account_id=j.account_id
LEFT JOIN LATERAL (SELECT r AS value FROM jsonb_array_elements(coalesce(o.recipient_snapshot,'[]'::jsonb)) r WHERE r->>'id'=item.subscription_id AND r->>'app_id'=j.app_id::text LIMIT 1) recipient ON true
LEFT JOIN event_fanout_recipients routed ON routed.outbox_id=o.id AND routed.subscription_id=item.subscription_id
LEFT JOIN invocations inv ON inv.id=(item.expected_progress->>'invocation_id')::uuid AND inv.account_id=j.account_id AND inv.app_id=j.app_id
LEFT JOIN platform_tenants tenant ON tenant.id=inv.platform_tenant_id AND tenant.account_id=j.account_id
LEFT JOIN event_delivery_slots slot ON slot.invocation_id=inv.id
LEFT JOIN consumers c ON c.app_id=slot.app_id AND c.subscription_id=slot.subscription_id
LEFT JOIN event_subscription_delivery_controls capacity_control ON capacity_control.account_id=j.account_id AND capacity_control.app_id=slot.app_id AND capacity_control.subscription_id::text=slot.subscription_id
CROSS JOIN totals
WHERE j.id=sqlc.arg(job_id)::uuid AND j.account_id=sqlc.arg(account_id)::uuid AND item.state='pending'
ORDER BY item.position LIMIT sqlc.arg(page_limit)::integer;

-- name: EventRecoveryPreflightJob :one
SELECT * FROM event_recovery_jobs WHERE id=sqlc.arg(job_id)::uuid AND account_id=sqlc.arg(account_id)::uuid;

-- name: EventRecoveryClaimExecutionNotification :one
SELECT j.id,j.account_id FROM event_recovery_jobs j
WHERE NOT j.execution_notification_captured AND j.selection->>'mode' IN ('execution','workflow')
 AND (j.state IN ('completed','cancelled') OR j.selection->>'mode'='workflow' AND j.state='expired') AND j.completed_at<=sqlc.arg(now_at)::timestamptz
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
SELECT coalesce(item.outbox_id,0)::bigint AS outbox_id,item.event_source,item.event_id,item.event_type,item.subscription_id,
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
SELECT j.id,coalesce(j.completed_at,j.created_at)::timestamptz AS completed_at,j.selection,j.state,j.execution_notification_captured,
 (SELECT count(*) FROM event_recovery_items i WHERE i.job_id=j.id AND i.state='queued') AS queued_count
FROM event_recovery_jobs j
WHERE j.account_id=sqlc.arg(account_id) AND j.app_id=sqlc.arg(app_id)
 AND (j.selection->>'mode'='workflow' OR j.state IN ('completed','cancelled') AND j.selection->>'mode'='execution')
 AND coalesce(j.completed_at,j.created_at)<=sqlc.arg(now_at) AND j.execution_finished_at IS NULL
 AND EXISTS (
 SELECT 1 FROM event_recovery_items i
 LEFT JOIN event_recovery_execution_results r ON r.job_id=i.job_id AND r.position=i.position
 AND r.replay_invocation_id=i.replay_invocation_id AND r.replay_generation=i.replay_generation
 AND r.replay_created_at=i.replay_created_at AND r.recorded_at<=sqlc.arg(now_at)
 LEFT JOIN event_recovery_workflow_results wr ON wr.job_id=i.job_id AND wr.position=i.position
 AND wr.run_id=(i.expected_progress->>'run_id')::uuid
 AND wr.resume_number=(i.expected_progress->>'expected_resume_count')::integer+1 AND wr.recorded_at<=sqlc.arg(now_at)
 WHERE i.job_id=j.id AND i.state='queued' AND (j.selection->>'mode'='execution' AND r.job_id IS NULL OR j.selection->>'mode'='workflow' AND wr.job_id IS NULL))
ORDER BY coalesce(j.completed_at,j.created_at),j.id LIMIT sqlc.arg(job_limit);

-- name: EventRecoveryNotificationEvidence :one
SELECT j.notification_receipts,j.execution_notification_captured,a.slug AS app_slug
FROM event_recovery_jobs j JOIN apps a ON a.id=j.app_id AND a.account_id=j.account_id
WHERE j.id=sqlc.arg(job_id)::uuid AND j.account_id=sqlc.arg(account_id)::uuid;

-- name: EventRecoveryNotificationOutbox :many
SELECT id,event,created_at,recipient_webhook_ids FROM app_webhook_event_outbox
WHERE source_id=sqlc.arg(job_id)::uuid AND account_id=sqlc.arg(account_id)::uuid AND app_id=sqlc.arg(app_id)::uuid
 AND event IN ('event_recovery.completed','event_recovery.cancelled','event_recovery.expired','event_recovery.execution_finished','event_recovery.execution_attention');

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

-- name: EventRecoveryNotificationRetryBacklogJobs :many
SELECT id,created_at FROM event_recovery_jobs
WHERE account_id=sqlc.arg(account_id)::uuid AND app_id=sqlc.arg(app_id)::uuid
 AND notification_retry_receipts <> '{}'::jsonb
 AND (NOT sqlc.arg(has_cursor)::boolean OR (created_at,id)<(sqlc.arg(cursor_created)::timestamptz,sqlc.arg(cursor_id)::uuid))
ORDER BY created_at DESC,id DESC LIMIT sqlc.arg(page_limit)::integer;

-- name: WorkflowRunRecoveryCandidate :one
SELECT 0::bigint AS outbox_id,''::text AS event_source,''::text AS event_id,''::text AS event_type,
 ''::text AS subscription_id,r.updated_at::timestamptz AS failed_at,r.status::text AS failure_code,false::boolean AS retryable,
 (jsonb_build_object('run_id',r.id::text,'expected_resume_count',sqlc.arg(expected_resume_count)::integer) || CASE WHEN sqlc.arg(failure_evidence_id)::text='' THEN '{}'::jsonb ELSE jsonb_build_object('failure_evidence_id',sqlc.arg(failure_evidence_id)::text) END)::jsonb AS expected_progress
FROM workflow_runs r JOIN apps a ON a.id=r.app_id
WHERE a.account_id=sqlc.arg(account_id)::uuid AND a.id=sqlc.arg(app_id)::uuid
 AND r.id=sqlc.arg(run_id)::uuid AND r.workflow_name=sqlc.arg(workflow_name)::text
 AND r.status IN ('failed','dead') AND r.resume_count=sqlc.arg(expected_resume_count)::integer
 AND (sqlc.arg(failure_evidence_id)::text='' OR r.resume_count=0 AND EXISTS (
 SELECT 1 FROM workflow_recovery_failure_evidence e WHERE e.id::text=sqlc.arg(failure_evidence_id)::text
 AND e.app_id=r.app_id AND e.run_id=r.id AND e.workflow=r.workflow_name AND e.resume_count=0
 AND e.failure_status=r.status AND e.observed_at>sqlc.arg(retained_after)::timestamptz
 AND e.observed_at<=sqlc.arg(observed_at)::timestamptz));

-- name: WorkflowRecoveryExecutionObservations :many
-- Native recovery observes only the generation admitted by the owning job.
SELECT item.position,
 (item.expected_progress->>'expected_resume_count')::integer AS expected_resume_count,
 coalesce(run.status,'')::text AS run_status,coalesce(run.resume_count,-1)::integer AS resume_count,
 run.updated_at AS run_updated_at,run.finished_at,
 coalesce(run.cancelled_at IS NOT NULL OR run.last_error='cancelled by operator',false)::boolean AS cancelled,
 admitted.created_at AS admitted_at,coalesce(result.state,'')::text AS result_state,
 result.completed_at AS result_completed_at,result.recorded_at AS result_recorded_at
FROM event_recovery_items item
JOIN event_recovery_jobs job ON job.id=item.job_id
JOIN apps app ON app.id=job.app_id AND app.account_id=job.account_id
LEFT JOIN workflow_runs run ON run.id=nullif(item.expected_progress->>'run_id','')::uuid
 AND run.app_id=job.app_id AND run.workflow_name=job.selection->'workflow_runs'->>'name'
LEFT JOIN workflow_run_resumes admitted ON admitted.run_id=run.id AND admitted.account_id=job.account_id
 AND admitted.resume_number=(item.expected_progress->>'expected_resume_count')::integer+1
 AND admitted.created_at>=job.created_at AND admitted.created_at<=sqlc.arg(now_at)::timestamptz
LEFT JOIN event_recovery_workflow_results result ON result.job_id=item.job_id AND result.position=item.position
 AND result.run_id=(item.expected_progress->>'run_id')::uuid
 AND result.resume_number=(item.expected_progress->>'expected_resume_count')::integer+1
 AND result.recorded_at<=sqlc.arg(now_at)::timestamptz
WHERE job.id=sqlc.arg(job_id)::uuid AND job.account_id=sqlc.arg(account_id)::uuid
 AND job.selection->>'mode'='workflow' AND item.state='queued'
 AND item.position>sqlc.arg(after_position)::bigint AND item.position<=sqlc.arg(through_position)::bigint
ORDER BY item.position;

-- name: WorkflowRunRecoveryRetryCandidate :one
SELECT 0::bigint AS outbox_id,''::text AS event_source,''::text AS event_id,''::text AS event_type,
 ''::text AS subscription_id,r.updated_at::timestamptz AS failed_at,r.status::text AS failure_code,false::boolean AS retryable,
 jsonb_build_object('run_id',r.id::text,'expected_resume_count',sqlc.arg(expected_resume_count)::integer,
 'parent_job_id',parent.id::text,'parent_position',item.position)::jsonb AS expected_progress
FROM event_recovery_jobs parent JOIN event_recovery_items item ON item.job_id=parent.id
JOIN event_recovery_workflow_results result ON result.job_id=item.job_id AND result.position=item.position
 AND result.run_id=(item.expected_progress->>'run_id')::uuid
 AND result.resume_number=(item.expected_progress->>'expected_resume_count')::integer+1
JOIN workflow_runs r ON r.id=result.run_id AND r.app_id=parent.app_id
WHERE parent.id=sqlc.arg(parent_job_id)::uuid AND parent.account_id=sqlc.arg(account_id)::uuid
 AND parent.app_id=sqlc.arg(app_id)::uuid AND parent.selection->>'mode'='workflow'
 AND parent.selection->'workflow_runs'->>'name'=sqlc.arg(workflow_name)::text
 AND parent.state IN ('completed','cancelled','expired') AND item.state='queued'
 AND item.position=sqlc.arg(parent_position)::bigint AND result.state='failed'
 AND result.recorded_at<=sqlc.arg(now_at)::timestamptz AND result.run_id=sqlc.arg(run_id)::uuid
 AND result.resume_number=sqlc.arg(expected_resume_count)::integer
 AND r.workflow_name=sqlc.arg(workflow_name)::text AND r.resume_count=result.resume_number
 AND r.status IN ('failed','dead');

-- name: WorkflowRecoveryClaimExecutionAttention :one
SELECT id,account_id FROM event_recovery_jobs
WHERE selection->>'mode'='workflow' AND execution_finished_at IS NULL
 AND EXISTS (SELECT 1 FROM event_recovery_items i WHERE i.job_id=event_recovery_jobs.id AND i.state='queued')
 AND NOT notification_receipts ? 'event_recovery.execution_attention'
 AND execution_attention_next_at<=sqlc.arg(now_at)::timestamptz
ORDER BY execution_attention_next_at,id LIMIT 1 FOR UPDATE SKIP LOCKED;

-- name: WorkflowRecoveryDeferExecutionAttention :exec
UPDATE event_recovery_jobs SET execution_attention_next_at=sqlc.arg(next_at)::timestamptz WHERE id=sqlc.arg(job_id)::uuid;

-- name: WorkflowRecoveryEnqueueExecutionAttention :exec
WITH recipients AS (
 SELECT coalesce(array_agg(h.id ORDER BY h.id),'{}'::uuid[]) AS ids FROM app_webhooks h
 JOIN event_recovery_jobs j ON j.id=sqlc.arg(job_id)::uuid AND h.account_id=j.account_id AND h.app_id=j.app_id
 WHERE h.scope='app' AND h.enabled AND (cardinality(h.event_filter)=0 OR 'event_recovery.execution_attention'=ANY(h.event_filter))
), captured AS (
 UPDATE event_recovery_jobs j SET notification_receipts=j.notification_receipts||jsonb_build_object('event_recovery.execution_attention',
 jsonb_build_object('event_id',sqlc.arg(event_id)::uuid,'captured_at',sqlc.arg(now_at)::timestamptz,'recipient_webhook_ids',to_jsonb(r.ids)))
 FROM recipients r WHERE j.id=sqlc.arg(job_id)::uuid AND NOT j.notification_receipts ? 'event_recovery.execution_attention'
 RETURNING j.id,j.account_id,j.app_id,r.ids
)
INSERT INTO app_webhook_event_outbox(id,account_id,app_id,event,source_id,payload,recipient_webhook_ids,created_at)
SELECT sqlc.arg(event_id)::uuid,j.account_id,j.app_id,'event_recovery.execution_attention',j.id,sqlc.arg(payload)::jsonb,j.ids,sqlc.arg(now_at)::timestamptz
FROM captured j WHERE cardinality(j.ids)>0 ON CONFLICT (event,source_id) DO NOTHING;

-- name: WorkflowRecoveryCancellationHistory :exec
INSERT INTO event_recovery_history(job_id,occurred_at,action,actor_kind,actor_id,reason,previous_state,state,previous_rate,rate,target_position,target_run_id,target_resume_count)
SELECT id,sqlc.arg(now_at)::timestamptz,'workflow_cancelled',sqlc.arg(actor_kind)::text,sqlc.arg(actor_id)::text,sqlc.arg(reason)::text,state,state,rate_per_second,rate_per_second,sqlc.arg(position)::bigint,sqlc.arg(run_id)::uuid,sqlc.arg(resume_count)::integer
FROM event_recovery_jobs WHERE id=sqlc.arg(job_id)::uuid;

-- name: WorkflowRecoveryActionReceiptGet :one
SELECT r.* FROM workflow_recovery_action_receipts r
JOIN event_recovery_jobs j ON j.id=r.job_id
WHERE j.account_id=sqlc.arg(account_id)::uuid AND r.job_id=sqlc.arg(job_id)::uuid AND r.request_id=sqlc.arg(request_id)::uuid;

-- name: WorkflowRecoveryActionReceiptCount :one
SELECT count(*) FROM workflow_recovery_action_receipts WHERE job_id=sqlc.arg(job_id)::uuid;

-- name: WorkflowRecoveryActionReceiptInsert :exec
INSERT INTO workflow_recovery_action_receipts(job_id,request_id,request,response,captured_at,actor_kind,actor_id)
VALUES(sqlc.arg(job_id)::uuid,sqlc.arg(request_id)::uuid,sqlc.arg(request)::jsonb,sqlc.arg(response)::jsonb,sqlc.arg(captured_at)::timestamptz,sqlc.arg(actor_kind)::text,sqlc.arg(actor_id)::text);

-- name: EventRecoveryByRequest :one
SELECT j.id FROM event_recovery_jobs j JOIN apps a ON a.id=j.app_id AND a.account_id=j.account_id
WHERE j.account_id=sqlc.arg(account_id)::uuid AND j.app_id=sqlc.arg(app_id)::uuid AND j.request_id=sqlc.arg(request_id)::uuid;

-- name: WorkflowRecoveryPolicyGet :one
SELECT p.* FROM workflow_recovery_policies p JOIN apps a ON a.id=p.app_id WHERE a.account_id=sqlc.arg(account_id)::uuid AND p.app_id=sqlc.arg(app_id)::uuid;

-- name: WorkflowRecoveryPolicySet :one
INSERT INTO workflow_recovery_policies(app_id,version,enabled,policy,updated_at,actor_kind,actor_id,next_evaluate_at,status)
SELECT a.id,1,sqlc.arg(enabled)::boolean,sqlc.arg(policy)::jsonb,sqlc.arg(now_at)::timestamptz,sqlc.arg(actor_kind)::text,sqlc.arg(actor_id)::text,sqlc.arg(now_at)::timestamptz,sqlc.arg(status)::text
FROM apps a WHERE a.id=sqlc.arg(app_id)::uuid AND a.account_id=sqlc.arg(account_id)::uuid AND a.status<>'deleted' AND sqlc.arg(expected_version)::bigint=0
ON CONFLICT(app_id) DO UPDATE SET version=workflow_recovery_policies.version+1,enabled=excluded.enabled,policy=excluded.policy,updated_at=excluded.updated_at,actor_kind=excluded.actor_kind,actor_id=excluded.actor_id,next_evaluate_at=excluded.next_evaluate_at,status=excluded.status,job_cursor=NULL,next_execute_at=excluded.next_evaluate_at,next_health_at=excluded.next_evaluate_at,automatic_failures=0,automatic_paused_at=NULL,automatic_pause_reason='',automatic_poll_failure=NULL
WHERE workflow_recovery_policies.version=sqlc.arg(expected_version)::bigint
RETURNING *;

-- name: WorkflowRecoveryPolicyUpdate :one
UPDATE workflow_recovery_policies p SET version=p.version+1,enabled=sqlc.arg(enabled)::boolean,policy=sqlc.arg(policy)::jsonb,updated_at=sqlc.arg(now_at)::timestamptz,actor_kind=sqlc.arg(actor_kind)::text,actor_id=sqlc.arg(actor_id)::text,next_evaluate_at=sqlc.arg(now_at)::timestamptz,status=sqlc.arg(status)::text,job_cursor=NULL,initial_failure_cursor=NULL,next_execute_at=sqlc.arg(now_at)::timestamptz,next_health_at=sqlc.arg(now_at)::timestamptz,automatic_failures=0,automatic_paused_at=NULL,automatic_pause_reason='',automatic_poll_failure=NULL
FROM apps a WHERE p.app_id=a.id AND a.id=sqlc.arg(app_id)::uuid AND a.account_id=sqlc.arg(account_id)::uuid AND a.status<>'deleted' AND p.version=sqlc.arg(expected_version)::bigint RETURNING p.*;

-- name: WorkflowRecoveryPolicyClaim :one
SELECT p.*,a.account_id,a.slug FROM workflow_recovery_policies p JOIN apps a ON a.id=p.app_id
WHERE p.enabled AND p.automatic_paused_at IS NULL AND a.status<>'deleted' AND p.next_evaluate_at<=sqlc.arg(now_at)::timestamptz
ORDER BY p.next_evaluate_at,p.app_id LIMIT 1 FOR UPDATE OF p SKIP LOCKED;

-- name: WorkflowRecoveryPolicyAdvance :exec
UPDATE workflow_recovery_policies SET next_evaluate_at=sqlc.arg(next_at)::timestamptz,last_evaluated_at=sqlc.arg(now_at)::timestamptz,job_cursor=sqlc.narg(job_cursor)::uuid,initial_failure_cursor=sqlc.narg(initial_failure_cursor)::uuid,status=sqlc.arg(status)::text WHERE app_id=sqlc.arg(app_id)::uuid;

-- name: WorkflowRecoveryPolicyJobs :many
SELECT id FROM event_recovery_jobs WHERE app_id=sqlc.arg(app_id)::uuid AND selection->'workflow_runs' IS NOT NULL AND selection->'workflow_runs'<>'null'::jsonb AND (sqlc.narg(after_id)::uuid IS NULL OR id>sqlc.narg(after_id)::uuid)
ORDER BY id LIMIT sqlc.arg(page_limit)::integer;

-- name: WorkflowRecoveryPolicyReviewCount :one
SELECT count(*) FROM workflow_recovery_policy_reviews WHERE app_id=sqlc.arg(app_id)::uuid;

-- name: WorkflowRecoveryPolicyBudget :one
SELECT count(*) FILTER(WHERE prepared_at>sqlc.arg(since_at)::timestamptz)::bigint AS hourly_actions,max(prepared_at)::timestamptz AS last_prepared_at FROM (SELECT app_id,prepared_at FROM workflow_recovery_policy_targets UNION ALL SELECT app_id,prepared_at FROM workflow_initial_recovery_reviews) charges WHERE app_id=sqlc.arg(app_id)::uuid;

-- name: WorkflowRecoveryPolicyTargets :many
SELECT run_id,resume_count,action FROM workflow_recovery_policy_targets WHERE app_id=sqlc.arg(app_id)::uuid AND run_id=ANY(sqlc.arg(run_ids)::uuid[]);

-- name: WorkflowRecoveryPolicyTargetInsert :exec
INSERT INTO workflow_recovery_policy_targets(app_id,review_id,run_id,resume_count,action,prepared_at) VALUES(sqlc.arg(app_id)::uuid,sqlc.arg(review_id)::uuid,sqlc.arg(run_id)::uuid,sqlc.arg(resume_count)::integer,sqlc.arg(action)::text,sqlc.arg(now_at)::timestamptz);

-- name: WorkflowRecoveryPolicyReviewInsert :execrows
INSERT INTO workflow_recovery_policy_reviews(id,app_id,job_id,fingerprint,review,observed_at) VALUES(sqlc.arg(id)::uuid,sqlc.arg(app_id)::uuid,sqlc.arg(job_id)::uuid,sqlc.arg(fingerprint)::text,sqlc.arg(review)::jsonb,sqlc.arg(now_at)::timestamptz) ON CONFLICT(app_id,job_id,fingerprint) DO NOTHING;

-- name: WorkflowRecoveryPolicyReviews :many
SELECT (r.review || COALESCE((SELECT jsonb_build_object('automatic_execution',jsonb_build_object('actions',jsonb_agg(jsonb_strip_nulls(jsonb_build_object('action_id',e.action_id,'position',e.position,'status',e.status,'reason',e.reason,'child_job_id',e.child_job_id,'applied_at',e.applied_at,'result',e.result,'suppression',e.suppression,'blocked_notification',e.blocked_notification,'last_evaluation',e.last_evaluation)) ORDER BY e.position))) FROM workflow_recovery_policy_executions e WHERE e.review_id=r.id HAVING count(*)>0),'{}'::jsonb) || CASE WHEN r.review_decision IS NULL THEN '{}'::jsonb ELSE jsonb_build_object('review_decision',r.review_decision) END || CASE WHEN n.receipt IS NULL THEN '{}'::jsonb ELSE jsonb_build_object('notification',n.receipt) END || CASE WHEN n.completion IS NULL THEN '{}'::jsonb ELSE jsonb_build_object('completion_notification',n.completion) END)::jsonb AS review,r.id FROM workflow_recovery_policy_reviews r LEFT JOIN workflow_recovery_review_notifications n ON n.review_id=r.id JOIN apps a ON a.id=r.app_id WHERE a.account_id=sqlc.arg(account_id)::uuid AND r.app_id=sqlc.arg(app_id)::uuid AND (sqlc.narg(after_id)::uuid IS NULL OR r.id>sqlc.narg(after_id)::uuid) ORDER BY r.id LIMIT sqlc.arg(page_limit)::integer;

-- name: WorkflowRecoveryPolicyReviewGet :one
SELECT (r.review || COALESCE((SELECT jsonb_build_object('automatic_execution',jsonb_build_object('actions',jsonb_agg(jsonb_strip_nulls(jsonb_build_object('action_id',e.action_id,'position',e.position,'status',e.status,'reason',e.reason,'child_job_id',e.child_job_id,'applied_at',e.applied_at,'result',e.result,'suppression',e.suppression,'blocked_notification',e.blocked_notification,'last_evaluation',e.last_evaluation)) ORDER BY e.position))) FROM workflow_recovery_policy_executions e WHERE e.review_id=r.id HAVING count(*)>0),'{}'::jsonb) || CASE WHEN r.review_decision IS NULL THEN '{}'::jsonb ELSE jsonb_build_object('review_decision',r.review_decision) END || CASE WHEN n.receipt IS NULL THEN '{}'::jsonb ELSE jsonb_build_object('notification',n.receipt) END || CASE WHEN n.completion IS NULL THEN '{}'::jsonb ELSE jsonb_build_object('completion_notification',n.completion) END)::jsonb AS review FROM workflow_recovery_policy_reviews r LEFT JOIN workflow_recovery_review_notifications n ON n.review_id=r.id JOIN apps a ON a.id=r.app_id WHERE a.account_id=sqlc.arg(account_id)::uuid AND r.app_id=sqlc.arg(app_id)::uuid AND r.id=sqlc.arg(id)::uuid;

-- name: WorkflowRecoveryPolicyReviewDecide :one
UPDATE workflow_recovery_policy_reviews r SET review_decision=COALESCE(r.review_decision,sqlc.arg(decision)::jsonb)
FROM apps a WHERE a.id=r.app_id AND a.account_id=sqlc.arg(account_id)::uuid AND r.app_id=sqlc.arg(app_id)::uuid AND r.id=sqlc.arg(id)::uuid AND r.review ? 'plan'
RETURNING (r.review || COALESCE((SELECT jsonb_build_object('automatic_execution',jsonb_build_object('actions',jsonb_agg(jsonb_strip_nulls(jsonb_build_object('action_id',e.action_id,'position',e.position,'status',e.status,'reason',e.reason,'child_job_id',e.child_job_id,'applied_at',e.applied_at,'result',e.result,'suppression',e.suppression,'blocked_notification',e.blocked_notification,'last_evaluation',e.last_evaluation)) ORDER BY e.position))) FROM workflow_recovery_policy_executions e WHERE e.review_id=r.id HAVING count(*)>0),'{}'::jsonb) || jsonb_build_object('review_decision',r.review_decision) || COALESCE((SELECT (CASE WHEN n.receipt IS NULL THEN '{}'::jsonb ELSE jsonb_build_object('notification',n.receipt) END || CASE WHEN n.completion IS NULL THEN '{}'::jsonb ELSE jsonb_build_object('completion_notification',n.completion) END) FROM workflow_recovery_review_notifications n WHERE n.review_id=r.id),'{}'::jsonb))::jsonb AS review;

-- name: WorkflowRecoveryReviewNotificationInsert :exec
INSERT INTO workflow_recovery_review_notifications(review_id,next_at) VALUES(sqlc.arg(review_id)::uuid,sqlc.arg(now_at)::timestamptz) ON CONFLICT DO NOTHING;

-- name: WorkflowRecoveryReviewNotificationClaim :one
SELECT n.review_id,r.app_id,a.account_id FROM workflow_recovery_review_notifications n JOIN workflow_recovery_policy_reviews r ON r.id=n.review_id JOIN apps a ON a.id=r.app_id
WHERE n.receipt IS NULL AND n.next_at<=sqlc.arg(now_at)::timestamptz
ORDER BY n.next_at,n.review_id LIMIT 1 FOR UPDATE OF n SKIP LOCKED;

-- name: WorkflowRecoveryReviewNotificationDefer :exec
UPDATE workflow_recovery_review_notifications SET next_at=sqlc.arg(next_at)::timestamptz WHERE review_id=sqlc.arg(review_id)::uuid AND receipt IS NULL;

-- name: WorkflowRecoveryReviewNotificationCapture :exec
UPDATE workflow_recovery_review_notifications SET receipt=sqlc.arg(receipt)::jsonb WHERE review_id=sqlc.arg(review_id)::uuid AND receipt IS NULL;

-- name: WorkflowRecoveryReviewNotificationReceivers :many
SELECT h.id FROM app_webhooks h WHERE h.scope='app' AND h.account_id=sqlc.arg(account_id)::uuid AND h.app_id=sqlc.arg(app_id)::uuid AND h.enabled AND (cardinality(h.event_filter)=0 OR sqlc.arg(event)::text=ANY(h.event_filter)) ORDER BY h.id;

-- name: WorkflowRecoveryReviewNotificationEnqueue :exec
INSERT INTO app_webhook_event_outbox(id,account_id,app_id,event,source_id,payload,recipient_webhook_ids,created_at)
VALUES(sqlc.arg(event_id)::uuid,sqlc.arg(account_id)::uuid,sqlc.arg(app_id)::uuid,sqlc.arg(event)::text,sqlc.arg(review_id)::uuid,sqlc.arg(payload)::jsonb,sqlc.arg(receiver_ids)::uuid[],sqlc.arg(now_at)::timestamptz) ON CONFLICT(event,source_id) DO NOTHING;

-- name: WorkflowRecoveryReviewCompletionClaim :one
SELECT n.review_id,r.app_id,a.account_id FROM workflow_recovery_review_notifications n JOIN workflow_recovery_policy_reviews r ON r.id=n.review_id JOIN apps a ON a.id=r.app_id
WHERE n.completion IS NULL AND n.next_completion_at<=sqlc.arg(now_at)::timestamptz ORDER BY n.next_completion_at,n.review_id LIMIT 1 FOR UPDATE OF n SKIP LOCKED;

-- name: WorkflowRecoveryReviewCompletionDefer :exec
UPDATE workflow_recovery_review_notifications SET next_completion_at=sqlc.arg(next_at)::timestamptz WHERE review_id=sqlc.arg(review_id)::uuid AND completion IS NULL;

-- name: WorkflowRecoveryReviewCompletionCapture :exec
UPDATE workflow_recovery_review_notifications SET completion=sqlc.arg(completion)::jsonb WHERE review_id=sqlc.arg(review_id)::uuid AND completion IS NULL;

-- name: WorkflowRecoveryAutomaticExecutionInsert :exec
INSERT INTO workflow_recovery_policy_executions(action_id,app_id,review_id,policy_version,position,reason) VALUES(sqlc.arg(action_id)::uuid,sqlc.arg(app_id)::uuid,sqlc.arg(review_id)::uuid,sqlc.arg(policy_version)::bigint,sqlc.arg(position)::bigint,'awaiting_execution') ON CONFLICT DO NOTHING;

-- name: WorkflowRecoveryAutomaticPolicyNext :one
SELECT p.app_id,a.account_id FROM workflow_recovery_policies p JOIN apps a ON a.id=p.app_id WHERE p.enabled AND p.policy->>'mode' IN ('automatic_retry','automatic_initial') AND p.automatic_paused_at IS NULL AND a.status<>'deleted' AND p.next_execute_at<=sqlc.arg(now_at)::timestamptz ORDER BY p.next_execute_at,p.app_id LIMIT 1;

-- name: WorkflowRecoveryAutomaticPolicyClaim :one
SELECT p.*,a.account_id,a.slug FROM workflow_recovery_policies p JOIN apps a ON a.id=p.app_id WHERE p.app_id=sqlc.arg(app_id)::uuid AND p.enabled AND p.policy->>'mode' IN ('automatic_retry','automatic_initial') AND p.automatic_paused_at IS NULL AND a.status<>'deleted' AND p.next_execute_at<=sqlc.arg(now_at)::timestamptz FOR UPDATE OF p SKIP LOCKED;

-- name: WorkflowRecoveryAutomaticPolicyAdvance :exec
UPDATE workflow_recovery_policies SET automatic_poll_failure=NULL,next_execute_at=sqlc.arg(next_at)::timestamptz,automatic_failures=sqlc.arg(failures)::integer,automatic_paused_at=sqlc.narg(paused_at)::timestamptz,automatic_pause_reason=sqlc.arg(pause_reason)::text,status=CASE WHEN sqlc.narg(paused_at)::timestamptz IS NOT NULL THEN 'paused' ELSE status END WHERE app_id=sqlc.arg(app_id)::uuid;

-- name: WorkflowRecoveryAutomaticObsolete :exec
UPDATE workflow_recovery_policy_executions SET status='suppressed',reason='policy_changed' WHERE app_id=sqlc.arg(app_id)::uuid AND status='pending' AND policy_version<>sqlc.arg(policy_version)::bigint;

-- name: WorkflowRecoveryAutomaticOutstanding :one
SELECT e.*,r.review FROM workflow_recovery_policy_executions e JOIN workflow_recovery_policy_reviews r ON r.id=e.review_id WHERE e.app_id=sqlc.arg(app_id)::uuid AND e.status='applied' AND e.result IS NULL ORDER BY e.applied_at,e.action_id LIMIT 1;

-- name: WorkflowRecoveryAutomaticPending :one
SELECT e.*, (r.review || CASE WHEN r.review_decision IS NULL THEN '{}'::jsonb ELSE jsonb_build_object('review_decision',r.review_decision) END)::jsonb AS review FROM workflow_recovery_policy_executions e JOIN workflow_recovery_policy_reviews r ON r.id=e.review_id WHERE e.app_id=sqlc.arg(app_id)::uuid AND e.policy_version=sqlc.arg(policy_version)::bigint AND e.status='pending' ORDER BY r.observed_at,e.review_id,e.position LIMIT 1 FOR UPDATE OF r,e;

-- name: WorkflowRecoveryAutomaticBudget :one
SELECT count(*) FILTER(WHERE applied_at>sqlc.arg(since_at)::timestamptz)::bigint AS hourly_executions,max(applied_at)::timestamptz AS last_execution_at FROM (SELECT app_id,applied_at FROM workflow_recovery_policy_execution_charges UNION ALL SELECT app_id,submitted_at AS applied_at FROM workflow_initial_recovery_reviews WHERE review->'policy'->>'mode'='automatic_initial' AND submitted_at IS NOT NULL) charges WHERE app_id=sqlc.arg(app_id)::uuid;

-- name: WorkflowRecoveryAutomaticApply :exec
WITH applied AS (UPDATE workflow_recovery_policy_executions SET status='applied',reason='child_created',child_job_id=sqlc.arg(child_job_id)::uuid,applied_at=sqlc.arg(now_at)::timestamptz WHERE action_id=sqlc.arg(action_id)::uuid AND status='pending' RETURNING action_id,app_id,applied_at)
INSERT INTO workflow_recovery_policy_execution_charges(action_id,app_id,applied_at) SELECT action_id,app_id,applied_at FROM applied ON CONFLICT DO NOTHING;

-- name: WorkflowRecoveryAutomaticSuppress :exec
UPDATE workflow_recovery_policy_executions SET status='suppressed',reason=sqlc.arg(reason)::text WHERE action_id=sqlc.arg(action_id)::uuid AND status='pending';

-- name: WorkflowRecoveryAutomaticResult :exec
UPDATE workflow_recovery_policy_executions SET result=sqlc.arg(result)::jsonb WHERE action_id=sqlc.arg(action_id)::uuid AND status='applied' AND result IS NULL;

-- name: WorkflowRecoveryAutomaticDefer :exec
UPDATE workflow_recovery_policy_executions SET reason=sqlc.arg(reason)::text WHERE action_id=sqlc.arg(action_id)::uuid AND status='pending';

-- name: WorkflowRecoveryAutomaticChargesPrune :exec
DELETE FROM workflow_recovery_policy_execution_charges WHERE app_id=sqlc.arg(app_id)::uuid AND applied_at<=sqlc.arg(before_at)::timestamptz;

-- name: WorkflowRecoveryPolicyControl :one
WITH controlled AS (UPDATE workflow_recovery_policies p SET
 automatic_paused_at=CASE WHEN sqlc.arg(operation)::text='pause' THEN sqlc.arg(now_at)::timestamptz ELSE NULL END,
 automatic_pause_reason=CASE WHEN sqlc.arg(operation)::text='pause' THEN 'operator: '||sqlc.arg(reason)::text ELSE '' END,
 automatic_failures=CASE WHEN sqlc.arg(operation)::text='resume' THEN 0 ELSE automatic_failures END,
 automatic_poll_failure=CASE WHEN sqlc.arg(operation)::text='resume' THEN NULL ELSE automatic_poll_failure END,
 status=CASE WHEN sqlc.arg(operation)::text='pause' THEN 'paused' ELSE 'monitoring' END,
 updated_at=sqlc.arg(now_at)::timestamptz, actor_kind=sqlc.arg(actor_kind)::text,actor_id=sqlc.arg(actor_id)::text,
 next_execute_at=sqlc.arg(now_at)::timestamptz,next_evaluate_at=sqlc.arg(now_at)::timestamptz
FROM apps a WHERE a.id=p.app_id AND a.account_id=sqlc.arg(account_id)::uuid AND a.status<>'deleted'
 AND p.app_id=sqlc.arg(app_id)::uuid AND p.version=sqlc.arg(expected_version)::bigint
 AND p.updated_at=sqlc.arg(expected_updated_at)::timestamptz
 AND p.automatic_paused_at IS NOT DISTINCT FROM sqlc.narg(expected_paused_at)::timestamptz
 AND p.enabled AND p.policy->>'mode' IN ('automatic_retry','automatic_initial')
RETURNING p.*), audited AS (
 INSERT INTO workflow_recovery_policy_controls(id,app_id,policy_version,operation,reason,actor_kind,actor_id,controlled_at)
 SELECT sqlc.arg(control_id)::uuid,app_id,version,sqlc.arg(operation)::text,sqlc.arg(reason)::text,sqlc.arg(actor_kind)::text,sqlc.arg(actor_id)::text,sqlc.arg(now_at)::timestamptz FROM controlled RETURNING id
)
SELECT controlled.* FROM controlled,audited;

-- name: WorkflowRecoveryAutomaticQueue :one
WITH charges AS (
 SELECT app_id,applied_at FROM workflow_recovery_policy_execution_charges
 UNION ALL SELECT app_id,submitted_at FROM workflow_initial_recovery_reviews WHERE review->'policy'->>'mode'='automatic_initial' AND submitted_at IS NOT NULL
), queue AS (
 SELECT r.observed_at,e.review_id,e.position,e.status,jsonb_build_object('queued_at',r.observed_at,'review_id',e.review_id,'policy_version',e.policy_version,'action',jsonb_strip_nulls(jsonb_build_object('action_id',e.action_id,'position',e.position,'status',e.status,'reason',e.reason,'child_job_id',e.child_job_id,'applied_at',e.applied_at,'suppression',e.suppression,'blocked_notification',e.blocked_notification,'last_evaluation',e.last_evaluation))) AS entry
 FROM workflow_recovery_policy_executions e JOIN workflow_recovery_policy_reviews r ON r.id=e.review_id
 WHERE e.app_id=sqlc.arg(app_id)::uuid AND ((e.status='pending' AND (sqlc.arg(all_pending)::boolean OR e.policy_version=sqlc.arg(policy_version)::bigint)) OR (e.status='applied' AND e.result IS NULL))
 UNION ALL
 SELECT r.prepared_at,r.id,1::bigint,CASE WHEN r.submitted_at IS NULL THEN 'pending' ELSE 'applied' END,
 jsonb_build_object('queued_at',r.prepared_at,'review_id',r.id,'policy_version',r.policy_version,'action',jsonb_strip_nulls(jsonb_build_object('action_id',r.id,'position',1,'status',CASE WHEN r.submitted_at IS NULL THEN 'pending' ELSE 'applied' END,'reason',COALESCE(r.review->'automatic_evaluation'->>'reason','awaiting_execution'),'child_job_id',r.job_id,'applied_at',r.submitted_at,'last_evaluation',r.review->'automatic_evaluation')))
 FROM workflow_initial_recovery_reviews r WHERE r.app_id=sqlc.arg(app_id)::uuid AND r.review->'policy'->>'mode'='automatic_initial'
 AND ((r.review->>'status'='prepared' AND (sqlc.arg(all_pending)::boolean OR r.policy_version=sqlc.arg(policy_version)::bigint)) OR (r.submitted_at IS NOT NULL AND NOT r.review ? 'automatic_result'))
)
SELECT jsonb_build_object(
 'pending',(SELECT count(*) FROM queue WHERE status='pending'),
 'outstanding',(SELECT count(*) FROM queue WHERE status='applied'),
 'hourly_executions',(SELECT count(*) FROM charges WHERE app_id=sqlc.arg(app_id)::uuid AND applied_at>sqlc.arg(since_at)::timestamptz),
 'last_execution_at',(SELECT max(applied_at) FROM charges WHERE app_id=sqlc.arg(app_id)::uuid),
 'entries',COALESCE((SELECT jsonb_agg(entry ORDER BY observed_at,review_id,position) FROM
 (SELECT * FROM queue ORDER BY observed_at,review_id,position LIMIT sqlc.arg(max_entries)::integer) page),'[]'::jsonb))::jsonb AS snapshot;

-- name: WorkflowRecoveryPolicyControlAudit :many
SELECT operation,reason,actor_kind,actor_id,controlled_at FROM workflow_recovery_policy_controls WHERE app_id=sqlc.arg(app_id)::uuid ORDER BY controlled_at DESC,id;

-- name: WorkflowRecoveryAutomaticQueueApp :one
SELECT id FROM apps WHERE id=sqlc.arg(app_id)::uuid AND account_id=sqlc.arg(account_id)::uuid AND status<>'deleted';

-- name: WorkflowRecoveryAutomaticOperatorPolicy :one
SELECT p.*,a.slug,a.account_id FROM workflow_recovery_policies p JOIN apps a ON a.id=p.app_id WHERE p.app_id=sqlc.arg(app_id)::uuid AND a.account_id=sqlc.arg(account_id)::uuid AND a.status<>'deleted' FOR UPDATE OF p;

-- name: WorkflowRecoveryAutomaticOperatorAction :one
SELECT * FROM workflow_recovery_policy_executions WHERE app_id=sqlc.arg(app_id)::uuid AND review_id=sqlc.arg(review_id)::uuid AND action_id=sqlc.arg(action_id)::uuid FOR UPDATE;

-- name: WorkflowRecoveryAutomaticOperatorSuppress :exec
UPDATE workflow_recovery_policy_executions SET status='suppressed',reason='operator_suppressed',suppression=sqlc.arg(suppression)::jsonb WHERE action_id=sqlc.arg(action_id)::uuid AND status='pending';

-- name: WorkflowRecoveryAutomaticBlockedNext :one
SELECT e.action_id,e.app_id,a.account_id,e.review_id FROM workflow_recovery_policy_executions e JOIN workflow_recovery_policies p ON p.app_id=e.app_id JOIN apps a ON a.id=e.app_id
WHERE a.status<>'deleted' AND e.next_blocked_at<=sqlc.arg(now_at)::timestamptz AND e.status IN ('pending','applied')
AND ((e.blocked_notification IS NOT NULL AND COALESCE(e.blocked_notification->>'state','blocked')<>'recovered')
OR (p.enabled AND p.policy->>'mode' IN ('automatic_retry','automatic_initial') AND p.automatic_paused_at IS NULL
AND ((e.status='pending' AND e.reason='evidence_unknown' AND e.policy_version=p.version) OR (e.status='applied' AND e.result IS NULL))))
ORDER BY e.next_blocked_at,e.app_id,e.action_id LIMIT 1;

-- name: WorkflowRecoveryAutomaticBlockedDefer :exec
UPDATE workflow_recovery_policy_executions SET next_blocked_at=sqlc.arg(next_at)::timestamptz WHERE action_id=sqlc.arg(action_id)::uuid;

-- name: WorkflowRecoveryAutomaticBlockedCapture :exec
UPDATE workflow_recovery_policy_executions SET blocked_notification=sqlc.arg(receipt)::jsonb WHERE action_id=sqlc.arg(action_id)::uuid;

-- name: WorkflowRecoveryAutomaticEvaluate :exec
UPDATE workflow_recovery_policy_executions SET last_evaluation=sqlc.arg(evaluation)::jsonb WHERE action_id=sqlc.arg(action_id)::uuid;

-- name: WorkflowRecoveryPolicyChangeJobs :many
SELECT id FROM event_recovery_jobs WHERE app_id=sqlc.arg(app_id)::uuid AND selection->'workflow_runs'->>'name'=ANY(sqlc.arg(workflow_names)::text[])
ORDER BY created_at DESC,id DESC LIMIT sqlc.arg(page_limit)::integer;

-- name: WorkflowRecoveryHealthClaim :one
SELECT p.app_id,a.account_id,a.slug FROM workflow_recovery_policies p JOIN apps a ON a.id=p.app_id LEFT JOIN workflow_recovery_health_incidents h ON h.app_id=p.app_id
WHERE a.status<>'deleted' AND p.next_health_at<=sqlc.arg(now_at)::timestamptz
AND (p.policy->'health_alerts'->>'enabled'='true' OR (h.app_id IS NOT NULL AND NOT h.incident ? 'closed_at'))
ORDER BY p.next_health_at,p.app_id LIMIT 1 FOR UPDATE OF p SKIP LOCKED;

-- name: WorkflowRecoveryHealthAdvance :exec
UPDATE workflow_recovery_policies SET next_health_at=sqlc.arg(next_at)::timestamptz WHERE app_id=sqlc.arg(app_id)::uuid;

-- name: WorkflowRecoveryHealthIncidentGet :one
SELECT incident FROM workflow_recovery_health_incidents WHERE app_id=sqlc.arg(app_id)::uuid;

-- name: WorkflowRecoveryHealthIncidentSet :exec
INSERT INTO workflow_recovery_health_incidents(app_id,incident) VALUES(sqlc.arg(app_id)::uuid,sqlc.arg(incident)::jsonb)
ON CONFLICT(app_id) DO UPDATE SET incident=excluded.incident;

-- name: WorkflowRecoveryHistory :many
WITH entries(event_key,occurred_at,kind,outcome,reason,policy_version,workflow,run_id,review_id,action_id,job_id,child_job_id,actor_kind,actor_id) AS (
SELECT 'policy:'||p.version::text,p.updated_at,'policy_observation',p.status,COALESCE(p.policy->>'reason',''),p.version,''::text,''::text,''::text,''::text,''::text,''::text,p.actor_kind,p.actor_id FROM workflow_recovery_policies p WHERE p.app_id=sqlc.arg(app_id)::uuid
UNION ALL
SELECT 'control:'||h.id::text,h.controlled_at,'policy_control',h.operation,h.reason,h.policy_version,''::text,''::text,''::text,''::text,''::text,''::text,h.actor_kind,h.actor_id FROM workflow_recovery_policy_controls h WHERE h.app_id=sqlc.arg(app_id)::uuid
UNION ALL
SELECT 'review:'||r.id::text||':'||(d->>'position'),r.observed_at,'review_outcome',COALESCE(d->>'outcome',''),COALESCE(d->>'trigger',''),(r.review->>'policy_version')::bigint,COALESCE(j.selection->'workflow_runs'->>'name',''),COALESCE(d->>'run_id',''),r.id::text,''::text,r.job_id::text,''::text,''::text,''::text FROM workflow_recovery_policy_reviews r JOIN event_recovery_jobs j ON j.id=r.job_id AND j.app_id=r.app_id CROSS JOIN LATERAL jsonb_array_elements(r.review->'decisions') d WHERE r.app_id=sqlc.arg(app_id)::uuid
UNION ALL
SELECT 'resolution:'||r.id::text||':'||(d->>'position'),(r.review_decision->>'decided_at')::timestamptz,'review_resolution',COALESCE(r.review_decision->>'decision',''),COALESCE(r.review_decision->>'reason',''),(r.review->>'policy_version')::bigint,COALESCE(j.selection->'workflow_runs'->>'name',''),COALESCE(d->>'run_id',''),r.id::text,''::text,r.job_id::text,''::text,COALESCE(r.review_decision->>'actor_kind',''),COALESCE(r.review_decision->>'actor_id','') FROM workflow_recovery_policy_reviews r JOIN event_recovery_jobs j ON j.id=r.job_id AND j.app_id=r.app_id CROSS JOIN LATERAL jsonb_array_elements(r.review->'decisions') d WHERE r.app_id=sqlc.arg(app_id)::uuid AND r.review_decision IS NOT NULL
UNION ALL
SELECT 'retry_applied:'||e.action_id::text,e.applied_at,'retry_applied','applied','',(r.review->>'policy_version')::bigint,COALESCE(j.selection->'workflow_runs'->>'name',''),COALESCE(d->>'run_id',''),r.id::text,e.action_id::text,r.job_id::text,COALESCE(e.child_job_id::text,''),'','' FROM workflow_recovery_policy_reviews r JOIN event_recovery_jobs j ON j.id=r.job_id AND j.app_id=r.app_id CROSS JOIN LATERAL jsonb_array_elements(r.review->'decisions') d JOIN workflow_recovery_policy_executions e ON e.review_id=r.id AND e.app_id=r.app_id AND e.position=(d->>'position')::bigint WHERE r.app_id=sqlc.arg(app_id)::uuid AND e.applied_at IS NOT NULL
UNION ALL
SELECT 'retry_suppressed:'||e.action_id::text,(e.suppression->>'suppressed_at')::timestamptz,'retry_suppressed','suppressed',COALESCE(e.suppression->>'reason',''),(r.review->>'policy_version')::bigint,COALESCE(j.selection->'workflow_runs'->>'name',''),COALESCE(d->>'run_id',''),r.id::text,e.action_id::text,r.job_id::text,COALESCE(e.child_job_id::text,''),COALESCE(e.suppression->>'actor_kind',''),COALESCE(e.suppression->>'actor_id','') FROM workflow_recovery_policy_reviews r JOIN event_recovery_jobs j ON j.id=r.job_id AND j.app_id=r.app_id CROSS JOIN LATERAL jsonb_array_elements(r.review->'decisions') d JOIN workflow_recovery_policy_executions e ON e.review_id=r.id AND e.app_id=r.app_id AND e.position=(d->>'position')::bigint WHERE r.app_id=sqlc.arg(app_id)::uuid AND e.suppression IS NOT NULL
UNION ALL
SELECT 'retry_evaluation:'||e.action_id::text,(e.last_evaluation->>'evaluated_at')::timestamptz,'retry_evaluation',e.status,COALESCE(e.last_evaluation->>'reason',''),(r.review->>'policy_version')::bigint,COALESCE(j.selection->'workflow_runs'->>'name',''),COALESCE(d->>'run_id',''),r.id::text,e.action_id::text,r.job_id::text,COALESCE(e.child_job_id::text,''),'','' FROM workflow_recovery_policy_reviews r JOIN event_recovery_jobs j ON j.id=r.job_id AND j.app_id=r.app_id CROSS JOIN LATERAL jsonb_array_elements(r.review->'decisions') d JOIN workflow_recovery_policy_executions e ON e.review_id=r.id AND e.app_id=r.app_id AND e.position=(d->>'position')::bigint WHERE r.app_id=sqlc.arg(app_id)::uuid AND e.last_evaluation IS NOT NULL
UNION ALL
SELECT 'retry_result:'||e.action_id::text,(e.result->>'recorded_at')::timestamptz,'retry_result',COALESCE(e.result->>'outcome',''),COALESCE(e.result->>'outcome_reason',''),(r.review->>'policy_version')::bigint,COALESCE(j.selection->'workflow_runs'->>'name',''),COALESCE(d->>'run_id',''),r.id::text,e.action_id::text,r.job_id::text,COALESCE(e.child_job_id::text,''),'','' FROM workflow_recovery_policy_reviews r JOIN event_recovery_jobs j ON j.id=r.job_id AND j.app_id=r.app_id CROSS JOIN LATERAL jsonb_array_elements(r.review->'decisions') d JOIN workflow_recovery_policy_executions e ON e.review_id=r.id AND e.app_id=r.app_id AND e.position=(d->>'position')::bigint WHERE r.app_id=sqlc.arg(app_id)::uuid AND e.result->>'recorded_at' IS NOT NULL
UNION ALL
SELECT 'retry_blocked_notification:'||e.action_id::text,(e.blocked_notification->>'captured_at')::timestamptz,'retry_blocked_notification',COALESCE(e.blocked_notification->>'status',''),COALESCE(e.blocked_notification->>'reason',''),(r.review->>'policy_version')::bigint,COALESCE(j.selection->'workflow_runs'->>'name',''),COALESCE(d->>'run_id',''),r.id::text,e.action_id::text,r.job_id::text,COALESCE(e.child_job_id::text,''),'','' FROM workflow_recovery_policy_reviews r JOIN event_recovery_jobs j ON j.id=r.job_id AND j.app_id=r.app_id CROSS JOIN LATERAL jsonb_array_elements(r.review->'decisions') d JOIN workflow_recovery_policy_executions e ON e.review_id=r.id AND e.app_id=r.app_id AND e.position=(d->>'position')::bigint WHERE r.app_id=sqlc.arg(app_id)::uuid AND e.blocked_notification IS NOT NULL
UNION ALL
SELECT 'health:'||(h.incident->>'episode_id'),(h.incident->>'observed_at')::timestamptz,'health_observation',h.incident->>'status',h.incident->>'reason',0::bigint,''::text,''::text,''::text,''::text,''::text,''::text,''::text,''::text FROM workflow_recovery_health_incidents h WHERE h.app_id=sqlc.arg(app_id)::uuid
UNION ALL
SELECT 'health_alert:'||(h.incident->'alert_notification'->>'event_id'),(h.incident->'alert_notification'->>'captured_at')::timestamptz,'health_alert',h.incident->'alert_notification'->>'status',''::text,0::bigint,''::text,''::text,''::text,''::text,''::text,''::text,''::text,''::text FROM workflow_recovery_health_incidents h WHERE h.app_id=sqlc.arg(app_id)::uuid AND h.incident ? 'alert_notification'
UNION ALL
SELECT 'health_recovered:'||(h.incident->'recovery_notification'->>'event_id'),(h.incident->'recovery_notification'->>'captured_at')::timestamptz,'health_recovered',h.incident->'recovery_notification'->>'status',''::text,0::bigint,''::text,''::text,''::text,''::text,''::text,''::text,''::text,''::text FROM workflow_recovery_health_incidents h WHERE h.app_id=sqlc.arg(app_id)::uuid AND h.incident ? 'recovery_notification'
), all_entries AS (
SELECT jsonb_build_object('id',event_key,'occurred_at',occurred_at,'kind',kind,'outcome',outcome,'reason',reason,'policy_version',policy_version,'workflow',workflow,'run_id',run_id,'review_id',review_id,'action_id',action_id,'job_id',job_id,'child_job_id',child_job_id,'actor_kind',actor_kind,'actor_id',actor_id)::jsonb AS entry,event_key,occurred_at,workflow,run_id,policy_version FROM entries
UNION ALL
SELECT h.entry,h.entry->>'id',h.occurred_at,COALESCE(h.entry->>'workflow',''),COALESCE(h.entry->>'run_id',''),COALESCE((h.entry->>'policy_version')::bigint,0)
FROM workflow_recovery_history_events h WHERE h.app_id=sqlc.arg(app_id)::uuid
AND h.sequence<=sqlc.arg(max_sequence)::bigint
AND h.recorded_at<=sqlc.arg(observed_at)::timestamptz AND h.recorded_at>sqlc.arg(retained_after)::timestamptz
UNION ALL
SELECT jsonb_build_object('id','retry_'||repairs.kind||'_evidence_repaired:'||e.action_id::text||':'||(receipt.value->>'request_id'),
'occurred_at',receipt.value->>'repaired_at','kind','retry_'||repairs.kind||'_evidence_repaired','outcome',receipt.value->>'outcome','reason',receipt.value->>'reason',
'policy_version',(r.review->>'policy_version')::bigint,'workflow',a->'retry'->'workflow_runs'->>'name','run_id',a->'retry'->'workflow_runs'->'runs'->0->>'run_id',
'review_id',r.id::text,'action_id',e.action_id::text,'job_id',r.job_id::text,'child_job_id',CASE WHEN repairs.kind='child' THEN COALESCE(e.child_job_id::text,'') ELSE '' END,
'actor_kind',receipt.value->>'actor_kind','actor_id',receipt.value->>'actor_id','evidence_kind',repairs.kind,'request_id',receipt.value->>'request_id',
'resume_count',(a->'retry'->'workflow_runs'->'runs'->0->>'expected_resume_count')::integer+CASE WHEN repairs.kind='child' THEN 1 ELSE 0 END,
'review_url','/v1/apps/'||(r.review->'plan'->>'app')||'/recovery-policy/reviews/'||r.id::text,
'evidence_url','/v1/apps/'||(r.review->'plan'->>'app')||'/recovery-policy/reviews/'||r.id::text||'/actions/'||e.action_id::text||'/evidence'||CASE WHEN repairs.kind='parent' THEN '/parent' ELSE '' END,
'job_url','/v1/event-recoveries/'||CASE WHEN repairs.kind='parent' THEN r.job_id::text ELSE e.child_job_id::text END),
'retry_'||repairs.kind||'_evidence_repaired:'||e.action_id::text||':'||(receipt.value->>'request_id'),(receipt.value->>'repaired_at')::timestamptz,
COALESCE(a->'retry'->'workflow_runs'->>'name',''),COALESCE(a->'retry'->'workflow_runs'->'runs'->0->>'run_id',''),(r.review->>'policy_version')::bigint
FROM workflow_recovery_policy_reviews r
CROSS JOIN LATERAL (VALUES ('parent',r.review->'parent_evidence_repairs'),('child',r.review->'evidence_repairs')) repairs(kind,receipts)
CROSS JOIN LATERAL jsonb_each(COALESCE(repairs.receipts,'{}'::jsonb)) receipt
JOIN workflow_recovery_policy_executions e ON e.review_id=r.id AND e.app_id=r.app_id AND e.action_id::text=receipt.key
CROSS JOIN LATERAL jsonb_array_elements(r.review->'plan'->'actions') a
WHERE r.app_id=sqlc.arg(app_id)::uuid AND (a->>'position')::bigint=e.position AND a->'retry'->'workflow_runs'->'runs'->0 IS NOT NULL
AND NOT EXISTS (SELECT 1 FROM workflow_recovery_history_events h WHERE h.app_id=r.app_id
AND h.entry->>'action_id'=e.action_id::text AND h.entry->>'kind'='retry_'||repairs.kind||'_evidence_repaired'
AND h.entry->>'request_id'=receipt.value->>'request_id')

UNION ALL
SELECT jsonb_build_object('id','initial_evidence_repaired:'||r.id::text||':'||(r.review->'evidence_repair'->>'request_id'),
'occurred_at',r.review->'evidence_repair'->>'repaired_at','kind','initial_evidence_repaired','outcome',r.review->'evidence_repair'->>'outcome','reason',r.review->'evidence_repair'->>'reason',
'policy_version',r.policy_version,'workflow',r.review->'failure'->>'workflow','run_id',r.review->'target'->>'run_id','review_id',r.id::text,'action_id',r.id::text,'job_id',r.review->>'job_id',
'actor_kind',r.review->'evidence_repair'->>'actor_kind','actor_id',r.review->'evidence_repair'->>'actor_id','evidence_kind','initial','request_id',r.review->'evidence_repair'->>'request_id',
'initial_failure_id',r.review->'failure'->>'id','resume_count',(r.review->'target'->>'expected_resume_count')::integer+1,
'review_url','/v1/apps/'||(r.review->>'app')||'/initial-recovery-reviews/'||r.id::text,
'evidence_url','/v1/apps/'||(r.review->>'app')||'/initial-recovery-reviews/'||r.id::text||'/evidence','job_url','/v1/event-recoveries/'||(r.review->>'job_id')),
'initial_evidence_repaired:'||r.id::text||':'||(r.review->'evidence_repair'->>'request_id'),(r.review->'evidence_repair'->>'repaired_at')::timestamptz,
COALESCE(r.review->'failure'->>'workflow',''),COALESCE(r.review->'target'->>'run_id',''),r.policy_version
FROM workflow_initial_recovery_reviews r WHERE r.app_id=sqlc.arg(app_id)::uuid AND r.review ? 'evidence_repair'
AND NOT EXISTS (SELECT 1 FROM workflow_recovery_history_events h WHERE h.app_id=r.app_id AND h.entry->>'kind'='initial_evidence_repaired' AND h.entry->>'review_id'=r.id::text AND h.entry->>'request_id'=r.review->'evidence_repair'->>'request_id')

)
SELECT entry FROM all_entries WHERE occurred_at>=sqlc.arg(start_at)::timestamptz AND occurred_at<=sqlc.arg(end_at)::timestamptz
AND (sqlc.arg(workflow)::text='' OR workflow=sqlc.arg(workflow)::text)
AND (sqlc.arg(run_id)::text='' OR run_id=sqlc.arg(run_id)::text)
AND (sqlc.arg(policy_version)::bigint=0 OR policy_version=sqlc.arg(policy_version)::bigint)
AND (sqlc.arg(after_key)::text='' OR (occurred_at,event_key COLLATE "C")<(sqlc.arg(after_at)::timestamptz,sqlc.arg(after_key)::text COLLATE "C"))
ORDER BY occurred_at DESC,event_key COLLATE "C" DESC LIMIT sqlc.arg(page_limit)::integer;



-- name: WorkflowRecoveryHistoryAppend :exec
INSERT INTO workflow_recovery_history_events(id,app_id,occurred_at,recorded_at,entry)
VALUES(sqlc.arg(id)::uuid,sqlc.arg(app_id)::uuid,sqlc.arg(occurred_at)::timestamptz,sqlc.arg(recorded_at)::timestamptz,sqlc.arg(entry)::jsonb);

-- name: WorkflowRecoveryHistoryPrune :exec
DELETE FROM workflow_recovery_history_events WHERE app_id=sqlc.arg(app_id)::uuid AND
(recorded_at<=sqlc.arg(before_at)::timestamptz OR id IN (
 SELECT id FROM workflow_recovery_history_events WHERE app_id=sqlc.arg(app_id)::uuid AND recorded_at>sqlc.arg(before_at)::timestamptz
 ORDER BY sequence DESC OFFSET sqlc.arg(keep_count)::integer
));

-- name: WorkflowRecoveryHistoryAction :one
SELECT e.app_id,jsonb_build_object('outcome',e.status,'reason',e.reason,'policy_version',e.policy_version,'workflow',COALESCE(j.selection->'workflow_runs'->>'name',''),'run_id',COALESCE(d->>'run_id',''),'review_id',r.id,'job_id',r.job_id,'action_id',e.action_id,'child_job_id',COALESCE(e.child_job_id::text,''))::jsonb AS entry
FROM workflow_recovery_policy_executions e JOIN workflow_recovery_policy_reviews r ON r.id=e.review_id AND r.app_id=e.app_id
JOIN event_recovery_jobs j ON j.id=r.job_id AND j.app_id=r.app_id
CROSS JOIN LATERAL jsonb_array_elements(r.review->'decisions') d
WHERE e.action_id=sqlc.arg(action_id)::uuid AND (d->>'position')::bigint=e.position;

-- name: WorkflowRecoveryHistoryExpire :execrows
DELETE FROM workflow_recovery_history_events WHERE id IN (
 SELECT id FROM workflow_recovery_history_events WHERE recorded_at<=sqlc.arg(before_at)::timestamptz
 ORDER BY recorded_at,id LIMIT sqlc.arg(batch_limit)::integer FOR UPDATE SKIP LOCKED
);

-- name: WorkflowRecoveryHistorySequence :one
SELECT COALESCE(max(sequence),0)::bigint FROM workflow_recovery_history_events WHERE app_id=sqlc.arg(app_id)::uuid;

-- name: WorkflowRecoveryPolicyEvaluationReviews :many
WITH durable AS (
 SELECT entry->>'id' AS evidence_id, entry->'candidate_evidence' AS evidence, COALESCE(entry->>'workflow','')::text AS workflow
 FROM workflow_recovery_history_events
 WHERE app_id=sqlc.arg(app_id)::uuid AND entry->>'kind'='candidates_observed'
 AND occurred_at>=sqlc.arg(start_at)::timestamptz AND occurred_at<=sqlc.arg(end_at)::timestamptz
 AND recorded_at>sqlc.arg(retained_after)::timestamptz AND recorded_at<=sqlc.arg(observed_at)::timestamptz
), reviews AS (
 SELECT jsonb_build_object('id',evidence->>'review_id','app_id',evidence->>'app_id','job_id',evidence->>'job_id','policy_version',evidence->'policy_version','policy',evidence->'policy','observed_at',evidence->'observed_at','decisions',evidence->'decisions') AS review, workflow, evidence_id FROM durable
 UNION ALL
 SELECT r.review, COALESCE(j.selection->'workflow_runs'->>'name','')::text AS workflow, ''::text AS evidence_id
 FROM workflow_recovery_policy_reviews r JOIN event_recovery_jobs j ON j.id=r.job_id AND j.app_id=r.app_id
 WHERE r.app_id=sqlc.arg(app_id)::uuid AND NOT EXISTS(SELECT 1 FROM durable d WHERE d.evidence->>'review_id'=r.id::text)
)
SELECT review, workflow, COALESCE(evidence_id,'')::text AS evidence_id FROM reviews
WHERE (review->>'observed_at')::timestamptz>=sqlc.arg(start_at)::timestamptz AND (review->>'observed_at')::timestamptz<=sqlc.arg(end_at)::timestamptz
 AND (sqlc.arg(workflow)::text='' OR workflow=sqlc.arg(workflow)::text)
ORDER BY (review->>'observed_at')::timestamptz,review->>'id' LIMIT sqlc.arg(page_limit)::integer;

-- name: WorkflowRecoveryPolicyEvaluationExecutions :many
SELECT entry FROM workflow_recovery_history_events
WHERE app_id=sqlc.arg(app_id)::uuid AND occurred_at>=sqlc.arg(start_at)::timestamptz AND occurred_at<=sqlc.arg(end_at)::timestamptz
 AND recorded_at>sqlc.arg(retained_after)::timestamptz AND recorded_at<=sqlc.arg(observed_at)::timestamptz
 AND entry->>'kind'='retry_transition' AND entry->>'evaluation_reason'='child_created'
ORDER BY occurred_at,id LIMIT sqlc.arg(page_limit)::integer;

-- name: WorkflowRecoveryFailureCaptureStart :one
SELECT started_at FROM workflow_recovery_failure_capture WHERE singleton;

-- name: WorkflowRecoveryFailureEvaluation :many
SELECT id,app_id,run_id,workflow,resume_count,failure_status,observed_at,completed_at,eligibility
FROM workflow_recovery_failure_evidence
WHERE app_id=sqlc.arg(app_id)::uuid AND observed_at>=sqlc.arg(start_at)::timestamptz AND observed_at<=sqlc.arg(end_at)::timestamptz
 AND observed_at>sqlc.arg(retained_after)::timestamptz AND observed_at<=sqlc.arg(observed_at)::timestamptz
 AND (sqlc.arg(workflow)::text='' OR workflow=sqlc.arg(workflow)::text)
ORDER BY observed_at,id LIMIT sqlc.arg(page_limit)::integer;

-- name: WorkflowRecoveryFailureExpire :execrows
WITH expired AS (
 SELECT id FROM workflow_recovery_failure_evidence WHERE observed_at<=sqlc.arg(before_at)::timestamptz
 ORDER BY observed_at,id FOR UPDATE SKIP LOCKED LIMIT sqlc.arg(batch_limit)::integer
)
DELETE FROM workflow_recovery_failure_evidence e USING expired WHERE e.id=expired.id;

-- name: WorkflowFailureEligibilityNext :one
SELECT e.*,a.account_id FROM workflow_recovery_failure_evidence e JOIN apps a ON a.id=e.app_id AND a.status<>'deleted'
WHERE e.observed_at>sqlc.arg(retained_after)::timestamptz AND e.observed_at<=sqlc.arg(now_at)::timestamptz
 AND (e.eligibility_observed_at IS NULL OR e.eligibility_observed_at<=sqlc.arg(refresh_before)::timestamptz)
ORDER BY e.eligibility_observed_at NULLS FIRST,e.observed_at,e.id
LIMIT 1 FOR UPDATE OF e SKIP LOCKED;

-- name: WorkflowFailureEligibilityParent :one
SELECT j.id,i.position FROM event_recovery_jobs j JOIN event_recovery_items i ON i.job_id=j.id
JOIN event_recovery_workflow_results r ON r.job_id=i.job_id AND r.position=i.position
WHERE j.app_id=sqlc.arg(app_id)::uuid AND j.account_id=sqlc.arg(account_id)::uuid
 AND j.selection->>'mode'='workflow' AND j.selection->'workflow_runs'->>'name'=sqlc.arg(workflow)::text
 AND j.state IN ('completed','cancelled','expired') AND j.created_at<=sqlc.arg(now_at)::timestamptz
 AND i.state='queued' AND r.run_id=sqlc.arg(run_id)::uuid AND r.resume_number=sqlc.arg(resume_count)::integer
 AND (i.expected_progress->>'run_id')::uuid=r.run_id
 AND (i.expected_progress->>'expected_resume_count')::integer+1=r.resume_number
 AND r.state='failed' AND r.recorded_at<=sqlc.arg(now_at)::timestamptz
ORDER BY j.created_at DESC,j.id DESC,i.position LIMIT 1;

-- name: WorkflowFailureEligibilitySave :exec
UPDATE workflow_recovery_failure_evidence SET eligibility=sqlc.arg(eligibility)::jsonb,
 eligibility_observed_at=sqlc.arg(observed_at)::timestamptz WHERE id=sqlc.arg(id)::uuid;

-- name: WorkflowInitialFailures :many
SELECT id,app_id,run_id,workflow,resume_count,failure_status,observed_at,completed_at,eligibility
FROM workflow_recovery_failure_evidence
WHERE app_id=sqlc.arg(app_id)::uuid AND resume_count=0
 AND observed_at>sqlc.arg(retained_after)::timestamptz AND observed_at<=sqlc.arg(observed_at)::timestamptz
 AND (sqlc.arg(workflow)::text='' OR workflow=sqlc.arg(workflow)::text)
 AND (sqlc.arg(after_id)::text='' OR (observed_at,id::text)<(sqlc.arg(after_at)::timestamptz,sqlc.arg(after_id)::text))
ORDER BY observed_at DESC,id DESC LIMIT sqlc.arg(page_limit)::integer;

-- name: WorkflowInitialFailure :one
SELECT id,app_id,run_id,workflow,resume_count,failure_status,observed_at,completed_at,eligibility
FROM workflow_recovery_failure_evidence
WHERE app_id=sqlc.arg(app_id)::uuid AND id=sqlc.arg(id)::uuid AND resume_count=0
 AND observed_at>sqlc.arg(retained_after)::timestamptz AND observed_at<=sqlc.arg(observed_at)::timestamptz;

-- name: WorkflowInitialReviewPolicy :one
SELECT p.*,a.slug FROM workflow_recovery_policies p JOIN apps a ON a.id=p.app_id
WHERE p.app_id=sqlc.arg(app_id)::uuid AND a.account_id=sqlc.arg(account_id)::uuid AND a.status<>'deleted' FOR SHARE OF p;

-- name: WorkflowInitialReviewGet :one
SELECT r.review FROM workflow_initial_recovery_reviews r JOIN apps a ON a.id=r.app_id
WHERE r.id=sqlc.arg(id)::uuid AND r.app_id=sqlc.arg(app_id)::uuid AND a.account_id=sqlc.arg(account_id)::uuid AND a.status<>'deleted';

-- name: WorkflowInitialReviewInsert :exec
INSERT INTO workflow_initial_recovery_reviews(id,app_id,failure_id,policy_version,prepared_at,expires_at,review)
VALUES(sqlc.arg(id)::uuid,sqlc.arg(app_id)::uuid,sqlc.arg(failure_id)::uuid,sqlc.arg(policy_version)::bigint,sqlc.arg(prepared_at)::timestamptz,sqlc.arg(expires_at)::timestamptz,sqlc.arg(review)::jsonb);

-- name: WorkflowInitialReviewUpdate :exec
UPDATE workflow_initial_recovery_reviews SET review=sqlc.arg(review)::jsonb,job_id=sqlc.narg(job_id)::uuid,submitted_at=sqlc.narg(submitted_at)::timestamptz WHERE id=sqlc.arg(id)::uuid;

-- name: WorkflowInitialReviewBudget :one
SELECT count(*)::bigint AS retained,count(*) FILTER(WHERE prepared_at>sqlc.arg(since_at)::timestamptz)::bigint AS prepared,
 count(*) FILTER(WHERE submitted_at>sqlc.arg(since_at)::timestamptz)::bigint AS submitted,
 max(prepared_at)::timestamptz AS last_prepared,max(submitted_at)::timestamptz AS last_submitted
FROM workflow_initial_recovery_reviews WHERE app_id=sqlc.arg(app_id)::uuid;

-- name: WorkflowInitialReviewPrune :exec
DELETE FROM workflow_initial_recovery_reviews WHERE app_id=sqlc.arg(app_id)::uuid AND prepared_at<=sqlc.arg(before_at)::timestamptz AND NOT (submitted_at IS NOT NULL AND NOT review ? 'automatic_result' AND NOT review ? 'result');

-- name: WorkflowInitialRecoveryPending :one
SELECT EXISTS(SELECT 1 FROM event_recovery_jobs j JOIN event_recovery_items i ON i.job_id=j.id
WHERE j.app_id=sqlc.arg(app_id)::uuid AND j.state IN ('running','paused') AND i.state='pending'
AND i.expected_progress->>'run_id'=sqlc.arg(run_id)::text AND (i.expected_progress->>'expected_resume_count')::integer=0)::boolean;

-- name: WorkflowInitialReviewAdmission :one
SELECT r.review,p.version,p.enabled,p.policy,p.automatic_paused_at FROM workflow_initial_recovery_reviews r JOIN workflow_recovery_policies p ON p.app_id=r.app_id
WHERE r.job_id=sqlc.arg(job_id)::uuid FOR SHARE OF p;

-- name: WorkflowInitialReviewList :many
SELECT r.review FROM workflow_initial_recovery_reviews r JOIN apps a ON a.id=r.app_id
WHERE r.app_id=sqlc.arg(app_id)::uuid AND a.account_id=sqlc.arg(account_id)::uuid AND a.status<>'deleted'
AND (r.prepared_at>sqlc.arg(retained_after)::timestamptz OR (r.submitted_at IS NOT NULL AND NOT r.review ? 'automatic_result' AND NOT r.review ? 'result')) AND (sqlc.narg(after_id)::uuid IS NULL OR r.id>sqlc.narg(after_id)::uuid)
ORDER BY r.id LIMIT sqlc.arg(page_limit)::integer;

-- name: WorkflowInitialReviewAuthority :one
SELECT r.review,p.version,p.enabled,p.policy,p.automatic_paused_at FROM workflow_initial_recovery_reviews r JOIN workflow_recovery_policies p ON p.app_id=r.app_id
WHERE r.job_id=sqlc.arg(job_id)::uuid;

-- name: WorkflowInitialRecoveryPolicyFailures :many
SELECT e.id FROM workflow_recovery_failure_evidence e
WHERE e.app_id=sqlc.arg(app_id)::uuid AND e.resume_count=0
 AND e.observed_at>sqlc.arg(retained_after)::timestamptz AND e.observed_at<=sqlc.arg(now_at)::timestamptz
 AND e.workflow=ANY(sqlc.arg(workflows)::text[]) AND e.failure_status=ANY(sqlc.arg(statuses)::text[])
 AND (sqlc.narg(after_id)::uuid IS NULL OR e.id>sqlc.narg(after_id)::uuid)
 AND NOT EXISTS (SELECT 1 FROM workflow_initial_recovery_reviews r WHERE r.app_id=e.app_id AND r.failure_id=e.id AND r.policy_version=sqlc.arg(policy_version)::bigint)
ORDER BY e.id LIMIT sqlc.arg(page_limit)::integer;

-- name: WorkflowInitialAutomaticOutstanding :one
SELECT review FROM workflow_initial_recovery_reviews
WHERE app_id=sqlc.arg(app_id)::uuid AND submitted_at IS NOT NULL AND review->'policy'->>'mode'='automatic_initial' AND NOT review ? 'automatic_result'
ORDER BY submitted_at,id LIMIT 1;

-- name: WorkflowInitialAutomaticPending :one
SELECT review FROM workflow_initial_recovery_reviews WHERE app_id=sqlc.arg(app_id)::uuid AND policy_version=sqlc.arg(policy_version)::bigint AND review->'policy'->>'mode'='automatic_initial' AND review->>'status'='prepared'
ORDER BY prepared_at,id LIMIT 1;

-- name: WorkflowInitialAutomaticUnresolved :one
SELECT EXISTS(SELECT 1 FROM workflow_initial_recovery_reviews WHERE app_id=sqlc.arg(app_id)::uuid AND submitted_at IS NOT NULL AND review->'policy'->>'mode'='automatic_initial' AND NOT review ? 'automatic_result')
 OR EXISTS(SELECT 1 FROM workflow_recovery_policy_executions WHERE app_id=sqlc.arg(app_id)::uuid AND status='applied' AND result IS NULL)
 OR EXISTS(SELECT 1 FROM event_recovery_jobs j JOIN event_recovery_items i ON i.job_id=j.id WHERE j.app_id=sqlc.arg(app_id)::uuid AND j.selection->'workflow_runs' IS NOT NULL AND j.selection->'workflow_runs'<>'null'::jsonb AND j.state IN ('running','paused') AND i.state='pending')
 OR EXISTS(SELECT 1 FROM event_recovery_jobs j JOIN event_recovery_items i ON i.job_id=j.id WHERE j.app_id=sqlc.arg(app_id)::uuid AND j.selection->>'mode'='workflow' AND i.state='queued'
 AND NOT EXISTS(SELECT 1 FROM event_recovery_workflow_results result WHERE result.job_id=j.id AND result.position=i.position AND result.run_id=(i.expected_progress->>'run_id')::uuid AND result.resume_number=(i.expected_progress->>'expected_resume_count')::integer+1)
 AND NOT EXISTS(SELECT 1 FROM workflow_runs run WHERE run.id=(i.expected_progress->>'run_id')::uuid AND run.app_id=j.app_id AND run.resume_count=(i.expected_progress->>'expected_resume_count')::integer+1 AND run.status IN ('succeeded','failed','dead')))
 OR EXISTS(SELECT 1 FROM workflow_runs WHERE app_id=sqlc.arg(app_id)::uuid AND resume_count>0 AND status IN ('pending','running','awaiting_event')) AS unresolved;

-- name: WorkflowInitialPolicyChangeFailures :many
SELECT id,app_id,run_id,workflow,failure_status,observed_at,completed_at
FROM workflow_recovery_failure_evidence
WHERE app_id=sqlc.arg(app_id)::uuid AND resume_count=0 AND observed_at>sqlc.arg(retained_after)::timestamptz AND observed_at<=sqlc.arg(now_at)::timestamptz
ORDER BY (workflow=ANY(sqlc.arg(workflows)::text[]) AND failure_status=ANY(sqlc.arg(statuses)::text[])) DESC,id ASC
LIMIT sqlc.arg(page_limit)::integer;

-- name: WorkflowInitialReviewRetainedCount :one
SELECT count(*) FROM workflow_initial_recovery_reviews WHERE app_id=sqlc.arg(app_id)::uuid
AND (prepared_at>sqlc.arg(retained_after)::timestamptz OR (submitted_at IS NOT NULL AND NOT review ? 'automatic_result' AND NOT review ? 'result'));

-- name: WorkflowInitialEvidenceRepair :execrows
INSERT INTO event_recovery_workflow_results(job_id,position,run_id,resume_number,admitted_at,state,completed_at,recorded_at)
SELECT j.id,i.position,r.id,r.resume_count,a.created_at,
 CASE WHEN r.status='succeeded' THEN 'succeeded' WHEN r.cancelled_at IS NOT NULL OR r.last_error='cancelled by operator' THEN 'cancelled' ELSE 'failed' END,
 r.finished_at,sqlc.arg(now_at)::timestamptz
FROM event_recovery_jobs j JOIN event_recovery_items i ON i.job_id=j.id AND i.position=1
JOIN apps app ON app.id=j.app_id AND app.account_id=j.account_id AND app.status<>'deleted'
JOIN workflow_runs r ON r.id=sqlc.arg(run_id)::uuid AND r.app_id=j.app_id AND r.workflow_name=j.selection->'workflow_runs'->>'name'
JOIN workflow_run_resumes a ON a.run_id=r.id AND a.account_id=j.account_id AND a.resume_number=1 AND a.created_at>=j.created_at
WHERE j.id=sqlc.arg(job_id)::uuid AND j.app_id=sqlc.arg(app_id)::uuid AND j.account_id=sqlc.arg(account_id)::uuid
 AND j.selection->>'mode'='workflow' AND i.state='queued'
 AND i.expected_progress->>'run_id'=r.id::text AND (i.expected_progress->>'expected_resume_count')::integer=0
 AND r.resume_count=1 AND r.status IN ('succeeded','failed','dead')
 AND r.updated_at BETWEEN a.created_at AND sqlc.arg(now_at)::timestamptz
 AND r.finished_at BETWEEN a.created_at AND sqlc.arg(now_at)::timestamptz
FOR SHARE OF r
ON CONFLICT(job_id,position) DO NOTHING;

-- name: WorkflowInitialEvidenceRepairWake :exec
UPDATE workflow_recovery_policies SET next_execute_at=LEAST(next_execute_at,sqlc.arg(now_at)::timestamptz) WHERE app_id=sqlc.arg(app_id)::uuid;

-- name: WorkflowInitialEvidenceResultExists :one
SELECT EXISTS(SELECT 1 FROM event_recovery_workflow_results WHERE job_id=sqlc.arg(job_id)::uuid AND position=1);

-- name: WorkflowRetryEvidenceRepair :execrows
INSERT INTO event_recovery_workflow_results(job_id,position,run_id,resume_number,admitted_at,state,completed_at,recorded_at)
SELECT j.id,i.position,r.id,r.resume_count,a.created_at,
 CASE WHEN r.status='succeeded' THEN 'succeeded' WHEN r.cancelled_at IS NOT NULL OR r.last_error='cancelled by operator' THEN 'cancelled' ELSE 'failed' END,
 r.finished_at,sqlc.arg(now_at)::timestamptz
FROM event_recovery_jobs j JOIN event_recovery_items i ON i.job_id=j.id AND i.position=1
JOIN apps app ON app.id=j.app_id AND app.account_id=j.account_id AND app.status<>'deleted'
JOIN workflow_runs r ON r.id=sqlc.arg(run_id)::uuid AND r.app_id=j.app_id AND r.workflow_name=j.selection->'workflow_runs'->>'name'
JOIN workflow_run_resumes a ON a.run_id=r.id AND a.account_id=j.account_id AND a.resume_number=sqlc.arg(expected_count)::integer+1 AND a.created_at>=j.created_at
WHERE j.id=sqlc.arg(job_id)::uuid AND j.app_id=sqlc.arg(app_id)::uuid AND j.account_id=sqlc.arg(account_id)::uuid
 AND j.selection->>'mode'='workflow' AND i.state='queued'
 AND i.expected_progress->>'run_id'=r.id::text AND (i.expected_progress->>'expected_resume_count')::integer=sqlc.arg(expected_count)::integer
 AND r.resume_count=sqlc.arg(expected_count)::integer+1 AND r.status IN ('succeeded','failed','dead')
 AND r.updated_at BETWEEN a.created_at AND sqlc.arg(now_at)::timestamptz
 AND r.finished_at BETWEEN a.created_at AND sqlc.arg(now_at)::timestamptz
 AND j.selection->>'parent_job_id'=sqlc.arg(parent_job_id)::uuid::text
 AND j.selection->>'request_id'=sqlc.arg(action_id)::uuid::text
 AND i.expected_progress->>'parent_job_id'=sqlc.arg(parent_job_id)::uuid::text
 AND (i.expected_progress->>'parent_position')::bigint=sqlc.arg(parent_position)::bigint
 AND NOT EXISTS(SELECT 1 FROM event_recovery_items other WHERE other.job_id=j.id AND other.position<>1)
 AND j.selection->'workflow_runs'->>'name'=sqlc.arg(workflow_name)::text
FOR SHARE OF r
ON CONFLICT(job_id,position) DO NOTHING;


-- name: WorkflowRetryEvidenceAction :one
SELECT * FROM workflow_recovery_policy_executions WHERE app_id=sqlc.arg(app_id)::uuid AND review_id=sqlc.arg(review_id)::uuid AND action_id=sqlc.arg(action_id)::uuid;

-- name: WorkflowRetryEvidenceReceipt :execrows
UPDATE workflow_recovery_policy_reviews SET review=jsonb_set(review,'{evidence_repairs}',COALESCE(review->'evidence_repairs','{}'::jsonb)||jsonb_build_object(sqlc.arg(action_id)::uuid::text,sqlc.arg(receipt)::jsonb),true)
WHERE id=sqlc.arg(review_id)::uuid AND app_id=sqlc.arg(app_id)::uuid
AND NOT COALESCE(review->'evidence_repairs','{}'::jsonb) ? sqlc.arg(action_id)::uuid::text;

-- name: WorkflowRetryParentEvidenceRepair :execrows
INSERT INTO event_recovery_workflow_results(job_id,position,run_id,resume_number,admitted_at,state,completed_at,recorded_at)
SELECT j.id,i.position,r.id,r.resume_count,a.created_at,
 CASE WHEN r.status='succeeded' THEN 'succeeded' WHEN r.cancelled_at IS NOT NULL OR r.last_error='cancelled by operator' THEN 'cancelled' ELSE 'failed' END,
 r.finished_at,sqlc.arg(now_at)::timestamptz
FROM event_recovery_jobs j JOIN event_recovery_items i ON i.job_id=j.id AND i.position=sqlc.arg(parent_position)::bigint
JOIN apps app ON app.id=j.app_id AND app.account_id=j.account_id AND app.status<>'deleted'
JOIN workflow_runs r ON r.id=sqlc.arg(run_id)::uuid AND r.app_id=j.app_id AND r.workflow_name=j.selection->'workflow_runs'->>'name'
JOIN workflow_run_resumes a ON a.run_id=r.id AND a.account_id=j.account_id AND a.resume_number=sqlc.arg(expected_count)::integer+1 AND a.created_at>=j.created_at
WHERE j.id=sqlc.arg(job_id)::uuid AND j.app_id=sqlc.arg(app_id)::uuid AND j.account_id=sqlc.arg(account_id)::uuid
 AND j.selection->>'mode'='workflow' AND i.state='queued'
 AND i.expected_progress->>'run_id'=r.id::text AND (i.expected_progress->>'expected_resume_count')::integer=sqlc.arg(expected_count)::integer
 AND r.resume_count=sqlc.arg(expected_count)::integer+1 AND r.status IN ('succeeded','failed','dead')
 AND r.updated_at BETWEEN a.created_at AND sqlc.arg(now_at)::timestamptz
 AND r.finished_at BETWEEN a.created_at AND sqlc.arg(now_at)::timestamptz
 AND j.selection->'workflow_runs'->>'name'=sqlc.arg(workflow_name)::text
FOR SHARE OF r
ON CONFLICT(job_id,position) DO NOTHING;



-- name: WorkflowRetryParentEvidenceReceipt :execrows
UPDATE workflow_recovery_policy_reviews SET review=jsonb_set(review,'{parent_evidence_repairs}',COALESCE(review->'parent_evidence_repairs','{}'::jsonb)||jsonb_build_object(sqlc.arg(action_id)::uuid::text,sqlc.arg(receipt)::jsonb),true)
WHERE id=sqlc.arg(review_id)::uuid AND app_id=sqlc.arg(app_id)::uuid
AND NOT COALESCE(review->'parent_evidence_repairs','{}'::jsonb) ? sqlc.arg(action_id)::uuid::text;

-- name: WorkflowRetryParentEvidenceResultExists :one
SELECT EXISTS(SELECT 1 FROM event_recovery_workflow_results WHERE job_id=sqlc.arg(job_id)::uuid AND position=sqlc.arg(position)::bigint);

-- name: WorkflowInitialManualCompletionNext :one
SELECT r.id,r.app_id,a.account_id,a.slug,r.policy_version FROM workflow_initial_recovery_reviews r JOIN apps a ON a.id=r.app_id
WHERE a.status<>'deleted' AND r.submitted_at IS NOT NULL AND r.review->'policy'->>'mode'='preview'
AND NOT r.review ? 'result' AND r.completion_next_observe_at<=sqlc.arg(now_at)::timestamptz
ORDER BY r.completion_next_observe_at,r.id LIMIT 1;

-- name: WorkflowInitialManualCompletionClaim :one
SELECT r.review FROM workflow_initial_recovery_reviews r JOIN apps a ON a.id=r.app_id
WHERE r.id=sqlc.arg(id)::uuid AND a.status<>'deleted' AND r.submitted_at IS NOT NULL AND r.review->'policy'->>'mode'='preview'
AND NOT r.review ? 'result' AND r.completion_next_observe_at<=sqlc.arg(now_at)::timestamptz FOR UPDATE OF r SKIP LOCKED;

-- name: WorkflowInitialManualCompletionAdvance :exec
UPDATE workflow_initial_recovery_reviews SET completion_next_observe_at=sqlc.arg(next_at)::timestamptz WHERE id=sqlc.arg(id)::uuid;

-- name: WorkflowInitialManualCompletionFailure :exec
UPDATE workflow_initial_recovery_reviews
SET review=jsonb_set(review,'{observation_failure}',sqlc.arg(failure)::jsonb,true),
completion_next_observe_at=sqlc.arg(next_at)::timestamptz
WHERE id=sqlc.arg(id)::uuid;

-- name: WorkflowRecoveryAutomaticPolicyFailure :exec
UPDATE workflow_recovery_policies SET automatic_poll_failure=sqlc.arg(failure)::jsonb,next_execute_at=sqlc.arg(next_at)::timestamptz WHERE app_id=sqlc.arg(app_id)::uuid;

-- name: WorkflowRecoveryAutomaticPollingNotificationSave :exec
UPDATE workflow_recovery_policies SET automatic_poll_notification=sqlc.arg(notification)::jsonb WHERE app_id=sqlc.arg(app_id)::uuid;

-- name: WorkflowInitialPollingNotificationSave :exec
UPDATE workflow_initial_recovery_reviews SET review=jsonb_set(review,'{polling_notification}',sqlc.arg(notification)::jsonb,true) WHERE id=sqlc.arg(id)::uuid;

-- name: WorkflowRecoveryOverviewManual :one
WITH candidates AS (
 SELECT r.review,r.submitted_at FROM workflow_initial_recovery_reviews r
 JOIN apps a ON a.id=r.app_id
 WHERE a.id=sqlc.arg(app_id)::uuid AND a.account_id=sqlc.arg(account_id)::uuid AND a.status<>'deleted'
 AND r.review->'policy'->>'mode'='preview'
 AND ((r.submitted_at IS NOT NULL AND NOT r.review ? 'result' AND NOT r.review ? 'automatic_result')
 OR (r.review->>'status'='prepared' AND r.submitted_at IS NULL AND r.expires_at>sqlc.arg(now_at)::timestamptz
 AND sqlc.arg(policy_enabled)::boolean AND r.policy_version=sqlc.arg(policy_version)::bigint))
), sample AS (SELECT review FROM candidates ORDER BY (review ? 'observation_failure') DESC,(review ? 'attention') DESC,review->>'id' LIMIT sqlc.arg(sample_limit)::integer)
SELECT jsonb_build_object(
 'awaiting',count(*) FILTER(WHERE submitted_at IS NULL),
 'unresolved',count(*) FILTER(WHERE submitted_at IS NOT NULL),
 'polling_failures',count(*) FILTER(WHERE review ? 'observation_failure'),
 'attention',count(*) FILTER(WHERE coalesce(review->'attention'->>'block_reason','')<>''),
 'reviews',coalesce((SELECT jsonb_agg(review) FROM sample),'[]'::jsonb))::jsonb FROM candidates;

-- name: WorkflowRecoveryOverviewLastAlert :one
SELECT (entry->'polling_notification')::jsonb AS notification
FROM workflow_recovery_history_events h JOIN apps a ON a.id=h.app_id
WHERE a.id=sqlc.arg(app_id)::uuid AND a.account_id=sqlc.arg(account_id)::uuid AND a.status<>'deleted'
AND h.recorded_at>sqlc.arg(retained_after)::timestamptz AND h.recorded_at<=sqlc.arg(now_at)::timestamptz
AND h.occurred_at<=sqlc.arg(now_at)::timestamptz AND entry->>'kind' IN ('polling_failed','polling_recovered')
ORDER BY h.occurred_at DESC,h.sequence DESC LIMIT 1;

-- name: WorkflowRecoveryOverviewDelivery :one
SELECT jsonb_build_object(
 'pending',count(*) FILTER(WHERE d.status='pending'),
 'in_flight',count(*) FILTER(WHERE d.status='in_flight'),
 'succeeded',count(*) FILTER(WHERE d.status='succeeded'),
 'failed',count(*) FILTER(WHERE d.status='failed'),
 'dead',count(*) FILTER(WHERE d.status='dead'),
 'missing',count(*) FILTER(WHERE d.id IS NULL),
 'relay_pending',EXISTS(SELECT 1 FROM app_webhook_event_outbox o WHERE o.id=sqlc.arg(event_id)::uuid AND o.account_id=sqlc.arg(account_id)::uuid AND o.app_id=sqlc.arg(app_id)::uuid AND o.event=sqlc.arg(event)::text)
)::jsonb
FROM unnest(sqlc.arg(receiver_ids)::uuid[]) frozen(id)
LEFT JOIN app_webhook_deliveries d ON d.webhook_id=frozen.id AND d.source_event_id=sqlc.arg(event_id)::uuid
AND d.account_id=sqlc.arg(account_id)::uuid AND d.app_id=sqlc.arg(app_id)::uuid AND d.event=sqlc.arg(event)::text;

-- name: WorkflowRecoveryOverviewApp :one
SELECT slug FROM apps WHERE id=sqlc.arg(app_id)::uuid AND account_id=sqlc.arg(account_id)::uuid AND status<>'deleted';

-- name: WorkflowRecoveryOverviewRetry :one
WITH candidates AS (
 SELECT r.id,r.job_id,r.review,
 CASE WHEN r.review_decision->>'decision'='approved' THEN 'awaiting_completion_observation' ELSE 'review_inspection_required' END AS reason
 FROM workflow_recovery_policy_reviews r
 JOIN apps a ON a.id=r.app_id
 JOIN event_recovery_jobs j ON j.id=r.job_id AND j.app_id=a.id AND j.account_id=a.account_id
 LEFT JOIN workflow_recovery_review_notifications n ON n.review_id=r.id
 WHERE a.id=sqlc.arg(app_id)::uuid AND a.account_id=sqlc.arg(account_id)::uuid AND a.status<>'deleted'
 AND r.review->'policy'->>'mode'='preview' AND EXISTS(SELECT 1 FROM jsonb_array_elements(r.review->'plan'->'actions') action WHERE action->>'kind'='retry')
 AND r.observed_at<=sqlc.arg(now_at)::timestamptz AND n.completion IS NULL
 AND (r.review->'completion_notification' IS NULL OR r.review->'completion_notification'='null'::jsonb)
 AND (r.review_decision->>'decision'='approved'
 OR (r.review_decision IS NULL AND sqlc.arg(policy_enabled)::boolean AND (r.review->>'policy_version')::bigint=sqlc.arg(policy_version)::bigint AND coalesce(n.receipt->>'status','')<>'suppressed'))
), sample AS (SELECT * FROM candidates ORDER BY id LIMIT sqlc.arg(sample_limit)::integer)
SELECT jsonb_build_object(
 'retry_awaiting',count(*) FILTER(WHERE reason='review_inspection_required'),
 'retry_unresolved',count(*) FILTER(WHERE reason='awaiting_completion_observation'),
 'retry_issues',coalesce((SELECT jsonb_agg(jsonb_build_object('scope','manual_retry','reason',reason,'policy_version',(review->>'policy_version')::bigint,'review_id',id,'job_id',job_id,'inspection_url','/v1/apps/'||sqlc.arg(escaped_slug)::text||'/recovery-policy/reviews/'||id::text||'/status') ORDER BY id) FROM sample),'[]'::jsonb))::jsonb FROM candidates;
