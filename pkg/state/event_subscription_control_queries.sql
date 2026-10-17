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

-- name: EventSubscriptionCapacitySet :exec
INSERT INTO event_subscription_delivery_controls(subscription_id,account_id,app_id,max_pending_deliveries,updated_at)
VALUES (sqlc.arg(subscription_id)::uuid,sqlc.arg(account_id)::uuid,sqlc.arg(app_id)::uuid,sqlc.arg(max_pending_deliveries)::integer,sqlc.arg(now_at)::timestamptz)
ON CONFLICT(subscription_id) DO UPDATE SET max_pending_deliveries=excluded.max_pending_deliveries,updated_at=excluded.updated_at
WHERE event_subscription_delivery_controls.account_id=excluded.account_id AND event_subscription_delivery_controls.app_id=excluded.app_id;

-- name: EventSubscriptionCapacityOverride :one
SELECT coalesce((SELECT max_pending_deliveries FROM event_subscription_delivery_controls
WHERE account_id=sqlc.arg(account_id)::uuid AND app_id=sqlc.arg(app_id)::uuid
AND subscription_id::text=sqlc.arg(subscription_id)::text),0)::integer AS max_pending_deliveries;

-- name: EventSubscriptionCapacityAccount :one
SELECT plan FROM accounts WHERE id=sqlc.arg(account_id)::uuid FOR SHARE;

-- name: EventSubscriptionCapacityCommandGet :one
SELECT * FROM event_subscription_capacity_commands WHERE account_id=sqlc.arg(account_id)::uuid AND app_id=sqlc.arg(app_id)::uuid
 AND subscription_id=sqlc.arg(subscription_id)::uuid AND command_id=sqlc.arg(command_id)::uuid AND retain_until>sqlc.arg(now_at)::timestamptz;

-- name: EventSubscriptionCapacityCommandsCount :one
SELECT count(*)::bigint FROM event_subscription_capacity_commands WHERE account_id=sqlc.arg(account_id)::uuid AND app_id=sqlc.arg(app_id)::uuid
 AND subscription_id=sqlc.arg(subscription_id)::uuid AND retain_until>sqlc.arg(now_at)::timestamptz;

-- name: EventSubscriptionCapacityCommandsExpire :exec
DELETE FROM event_subscription_capacity_commands WHERE account_id=sqlc.arg(account_id)::uuid AND app_id=sqlc.arg(app_id)::uuid
 AND subscription_id=sqlc.arg(subscription_id)::uuid AND retain_until<=sqlc.arg(now_at)::timestamptz;

-- name: EventSubscriptionCapacityCommandInsert :exec
INSERT INTO event_subscription_capacity_commands(subscription_id,command_id,account_id,app_id,request,receipt,recorded_at,retain_until)
VALUES(sqlc.arg(subscription_id)::uuid,sqlc.arg(command_id)::uuid,sqlc.arg(account_id)::uuid,sqlc.arg(app_id)::uuid,sqlc.arg(request)::jsonb,sqlc.arg(receipt)::jsonb,sqlc.arg(recorded_at)::timestamptz,sqlc.arg(retain_until)::timestamptz);

-- name: PruneEventSubscriptionCapacityCommands :execrows
WITH expired AS (
 SELECT subscription_id,command_id FROM event_subscription_capacity_commands WHERE retain_until<=sqlc.arg(now_at)::timestamptz
 ORDER BY retain_until,subscription_id,command_id FOR UPDATE SKIP LOCKED LIMIT sqlc.arg(batch_limit)::integer
)
DELETE FROM event_subscription_capacity_commands c USING expired e WHERE c.subscription_id=e.subscription_id AND c.command_id=e.command_id;

-- name: ListEventSubscriptionCapacityCommands :many
SELECT * FROM event_subscription_capacity_commands
WHERE account_id=sqlc.arg(account_id)::uuid AND app_id=sqlc.arg(app_id)::uuid AND subscription_id=sqlc.arg(subscription_id)::uuid
 AND retain_until>sqlc.arg(now_at)::timestamptz AND recorded_at<=sqlc.arg(now_at)::timestamptz
 AND (sqlc.arg(outcome)::text='any' OR receipt->>'outcome'=sqlc.arg(outcome)::text)
 AND (NOT sqlc.arg(has_after)::boolean OR (recorded_at,command_id)<(sqlc.arg(after_recorded_at)::timestamptz,sqlc.arg(after_command_id)::uuid))
ORDER BY recorded_at DESC,command_id DESC LIMIT sqlc.arg(page_limit)::integer;

-- name: EventSubscriptionCapacityCommandBudget :one
SELECT count(*)::bigint AS retained_receipts,min(retain_until)::timestamptz AS earliest_expiry_at
FROM event_subscription_capacity_commands
WHERE account_id=sqlc.arg(account_id)::uuid AND app_id=sqlc.arg(app_id)::uuid AND subscription_id=sqlc.arg(subscription_id)::uuid
 AND retain_until>sqlc.arg(observed_at)::timestamptz;

-- name: EventSubscriptionRateClock :one
SELECT clock_timestamp()::timestamptz;

-- name: EventSubscriptionRateInitialize :exec
INSERT INTO event_subscription_delivery_controls(account_id,app_id,subscription_id) VALUES(sqlc.arg(account_id)::uuid,sqlc.arg(app_id)::uuid,sqlc.arg(subscription_id)::uuid) ON CONFLICT(subscription_id) DO NOTHING;

-- name: EventSubscriptionRateConfigure :one
UPDATE event_subscription_delivery_controls SET
 rate_enabled=CASE WHEN sqlc.arg(applied)::boolean THEN sqlc.arg(enabled)::boolean ELSE rate_enabled END,
 rate_interval_ms=CASE WHEN sqlc.arg(applied)::boolean THEN sqlc.arg(interval_ms)::bigint ELSE rate_interval_ms END,
 rate_burst=CASE WHEN sqlc.arg(applied)::boolean THEN sqlc.arg(burst)::bigint ELSE rate_burst END,
 rate_credit_ms=CASE WHEN sqlc.arg(applied)::boolean THEN sqlc.arg(credit_ms)::bigint ELSE rate_credit_ms END,
 rate_refilled_at=CASE WHEN sqlc.arg(applied)::boolean THEN sqlc.arg(now_at)::timestamptz ELSE rate_refilled_at END,
 rate_revision=rate_revision+CASE WHEN sqlc.arg(applied)::boolean THEN 1 ELSE 0 END,
 rate_sequence=rate_sequence+1,updated_at=CASE WHEN sqlc.arg(applied)::boolean THEN sqlc.arg(now_at)::timestamptz ELSE updated_at END
WHERE account_id=sqlc.arg(account_id)::uuid AND app_id=sqlc.arg(app_id)::uuid AND subscription_id=sqlc.arg(subscription_id)::uuid RETURNING *;

-- name: EventSubscriptionRateConsume :exec
UPDATE event_subscription_delivery_controls SET rate_credit_ms=sqlc.arg(credit_ms)::bigint,rate_refilled_at=sqlc.arg(refilled_at)::timestamptz WHERE subscription_id=sqlc.arg(subscription_id)::uuid;

-- name: EventSubscriptionRateCommandGet :one
SELECT * FROM event_subscription_rate_commands WHERE account_id=sqlc.arg(account_id)::uuid AND app_id=sqlc.arg(app_id)::uuid AND subscription_id=sqlc.arg(subscription_id)::uuid AND command_id=sqlc.arg(command_id)::uuid AND retain_until>sqlc.arg(now_at)::timestamptz;

-- name: EventSubscriptionRateCommandsExpire :exec
DELETE FROM event_subscription_rate_commands WHERE subscription_id=sqlc.arg(subscription_id)::uuid AND retain_until<=sqlc.arg(now_at)::timestamptz;

-- name: EventSubscriptionRateCommandsCount :one
SELECT count(*)::bigint FROM event_subscription_rate_commands WHERE subscription_id=sqlc.arg(subscription_id)::uuid AND retain_until>sqlc.arg(now_at)::timestamptz;

-- name: EventSubscriptionRateCommandInsert :exec
INSERT INTO event_subscription_rate_commands(account_id,app_id,subscription_id,command_id,sequence,request,receipt,recorded_at,retain_until) VALUES(sqlc.arg(account_id)::uuid,sqlc.arg(app_id)::uuid,sqlc.arg(subscription_id)::uuid,sqlc.arg(command_id)::uuid,sqlc.arg(sequence)::bigint,sqlc.arg(request)::jsonb,sqlc.arg(receipt)::jsonb,sqlc.arg(now_at)::timestamptz,sqlc.arg(retain_until)::timestamptz);

-- name: EventSubscriptionRateCommandsList :many
SELECT receipt FROM event_subscription_rate_commands WHERE account_id=sqlc.arg(account_id)::uuid AND app_id=sqlc.arg(app_id)::uuid AND subscription_id=sqlc.arg(subscription_id)::uuid AND sequence>sqlc.arg(after_sequence)::bigint AND retain_until>sqlc.arg(now_at)::timestamptz ORDER BY sequence LIMIT sqlc.arg(page_limit)::integer;

-- name: EventSubscriptionRatePreviewPending :many
SELECT b.outbox_id,b.accepted_at,b.next_attempt_at,b.lease_until::timestamptz AS lease_until,
 coalesce(o.payload->>'source','')::text AS event_source,coalesce(o.payload->>'id','')::text AS event_id,
 event_recipient_delivery_deadline(coalesce(r.recipient,(SELECT v FROM jsonb_array_elements(o.recipient_snapshot) v WHERE v->>'id'=b.subscription_id LIMIT 1)),o.created_at,o.recipient_progress->b.subscription_id)::timestamptz AS deadline_at
FROM event_routing_backlog_source b JOIN event_fanout_outbox o ON o.id=b.outbox_id
LEFT JOIN event_fanout_recipients r ON r.outbox_id=b.outbox_id AND r.subscription_id=b.subscription_id
WHERE b.account_id=sqlc.arg(account_id)::uuid AND b.app_id=sqlc.arg(app_id)::uuid AND b.subscription_id=sqlc.arg(subscription_id)::text AND b.consumer_kind='application' AND b.routing_state='pending'
ORDER BY b.accepted_at,b.outbox_id LIMIT sqlc.arg(sample_limit)::integer;

-- name: EventSubscriptionPendingSummaryBudget :one
SELECT set_config('statement_timeout',sqlc.arg(timeout_millis)::text,true)::text;

-- name: EventSubscriptionPendingSummary :one
WITH pending AS (
 SELECT b.accepted_at,b.next_attempt_at,b.lease_until,
 event_recipient_delivery_deadline(coalesce(r.recipient,(SELECT v FROM jsonb_array_elements(o.recipient_snapshot) v WHERE v->>'id'=b.subscription_id LIMIT 1)),o.created_at,o.recipient_progress->b.subscription_id)::timestamptz AS deadline_at
 FROM event_routing_backlog_source b JOIN event_fanout_outbox o ON o.id=b.outbox_id
 LEFT JOIN event_fanout_recipients r ON r.outbox_id=b.outbox_id AND r.subscription_id=b.subscription_id
 WHERE b.account_id=sqlc.arg(account_id)::uuid AND b.app_id=sqlc.arg(app_id)::uuid AND b.subscription_id=sqlc.arg(subscription_id)::text AND b.consumer_kind='application' AND b.routing_state='pending'
)
SELECT count(*)::bigint AS pending_recipients,
 count(deadline_at)::bigint AS deadline_recipients,
 count(*) FILTER (WHERE deadline_at<=sqlc.arg(observed_at)::timestamptz)::bigint AS already_expired_count,
 min(deadline_at) FILTER (WHERE isfinite(deadline_at))::timestamptz AS earliest_deadline_at,
 coalesce(bool_or(accepted_at IS NULL OR NOT isfinite(accepted_at) OR accepted_at>sqlc.arg(observed_at)::timestamptz OR (deadline_at IS NOT NULL AND NOT isfinite(deadline_at)) OR (next_attempt_at IS NOT NULL AND NOT isfinite(next_attempt_at)) OR (lease_until IS NOT NULL AND NOT isfinite(lease_until))),false)::boolean AS clock_invalid,
 coalesce(bool_or(lease_until>sqlc.arg(observed_at)::timestamptz),false)::boolean AS active_leases,
 coalesce(bool_or(next_attempt_at>sqlc.arg(observed_at)::timestamptz),false)::boolean AS scheduled_retries
FROM pending;

-- name: EventSubscriptionDeadlinePage :many
WITH deadlines AS (
 SELECT b.outbox_id,b.accepted_at,b.next_attempt_at,b.lease_until::timestamptz AS lease_until,b.routing_mode,b.capacity_scope,
 o.source,o.event_id,
 event_recipient_delivery_deadline(coalesce(r.recipient,(SELECT v FROM jsonb_array_elements(o.recipient_snapshot) v WHERE v->>'id'=b.subscription_id LIMIT 1)),o.created_at,o.recipient_progress->b.subscription_id)::timestamptz AS deadline_at
 FROM event_routing_backlog_source b JOIN event_fanout_outbox o ON o.id=b.outbox_id AND o.account_id=b.account_id
 LEFT JOIN event_fanout_recipients r ON r.outbox_id=b.outbox_id AND r.subscription_id=b.subscription_id
 WHERE b.account_id=sqlc.arg(account_id)::uuid AND b.app_id=sqlc.arg(app_id)::uuid AND b.subscription_id=sqlc.arg(subscription_id)::text AND b.consumer_kind='application' AND b.routing_state='pending' AND b.accepted_at<=sqlc.arg(window_at)::timestamptz
), page AS MATERIALIZED (
 SELECT * FROM deadlines WHERE deadline_at IS NOT NULL
 AND (sqlc.arg(within_millis)::bigint=0 OR deadline_at<sqlc.arg(window_at)::timestamptz+sqlc.arg(within_millis)::bigint*interval '1 millisecond')
 AND (sqlc.arg(waiting_reason)::text='' OR (CASE WHEN NOT (routing_mode='event' AND coalesce(lease_until>sqlc.arg(observed_at)::timestamptz,false)) AND event_subscription_delivery_waiting_reason(sqlc.arg(account_id)::uuid,sqlc.arg(app_id)::uuid,sqlc.arg(subscription_id)::text,sqlc.arg(observed_at)::timestamptz)<>'' THEN event_subscription_delivery_waiting_reason(sqlc.arg(account_id)::uuid,sqlc.arg(app_id)::uuid,sqlc.arg(subscription_id)::text,sqlc.arg(observed_at)::timestamptz) ELSE event_backlog_waiting_reason('pending',capacity_scope,routing_mode,lease_until,next_attempt_at,'application',NULL,sqlc.arg(observed_at)::timestamptz) END)=sqlc.arg(waiting_reason)::text)
 AND (sqlc.narg(deadline_from)::timestamptz IS NULL OR deadline_at>=sqlc.narg(deadline_from)::timestamptz)
 AND (sqlc.narg(deadline_before)::timestamptz IS NULL OR deadline_at<sqlc.narg(deadline_before)::timestamptz)
 AND (sqlc.arg(status_filter)::text<>'expired' OR deadline_at<=sqlc.arg(window_at)::timestamptz)
 AND (sqlc.arg(status_filter)::text<>'upcoming' OR deadline_at>sqlc.arg(window_at)::timestamptz)
 AND (NOT sqlc.arg(has_after)::boolean OR (deadline_at,accepted_at,outbox_id)>(sqlc.arg(after_deadline_at)::timestamptz,sqlc.arg(after_accepted_at)::timestamptz,sqlc.arg(after_outbox_id)::bigint))
 ORDER BY deadline_at,accepted_at,outbox_id LIMIT sqlc.arg(page_limit)::integer
)
SELECT page.*,(CASE WHEN NOT (page.routing_mode='event' AND coalesce(page.lease_until>sqlc.arg(observed_at)::timestamptz,false)) AND control.reason<>'' THEN control.reason ELSE event_backlog_waiting_reason('pending',page.capacity_scope,page.routing_mode,page.lease_until,page.next_attempt_at,'application',NULL,sqlc.arg(observed_at)::timestamptz) END)::text AS waiting_reason
FROM page CROSS JOIN LATERAL (SELECT event_subscription_delivery_waiting_reason(sqlc.arg(account_id)::uuid,sqlc.arg(app_id)::uuid,sqlc.arg(subscription_id)::text,sqlc.arg(observed_at)::timestamptz) AS reason) control
ORDER BY page.deadline_at,page.accepted_at,page.outbox_id;

-- name: EventSubscriptionDeadlineWindowCounts :many
WITH pending AS (
 SELECT b.accepted_at,b.next_attempt_at,b.lease_until,b.routing_mode,b.capacity_scope,
 event_recipient_delivery_deadline(coalesce(r.recipient,(SELECT v FROM jsonb_array_elements(o.recipient_snapshot) v WHERE v->>'id'=b.subscription_id LIMIT 1)),o.created_at,o.recipient_progress->b.subscription_id)::timestamptz AS deadline_at
 FROM event_routing_backlog_source b JOIN event_fanout_outbox o ON o.id=b.outbox_id AND o.account_id=b.account_id
 LEFT JOIN event_fanout_recipients r ON r.outbox_id=b.outbox_id AND r.subscription_id=b.subscription_id
 WHERE b.account_id=sqlc.arg(account_id)::uuid AND b.app_id=sqlc.arg(app_id)::uuid AND b.subscription_id=sqlc.arg(subscription_id)::text AND b.consumer_kind='application' AND b.routing_state='pending'
), control AS MATERIALIZED (
 SELECT event_subscription_delivery_waiting_reason(sqlc.arg(account_id)::uuid,sqlc.arg(app_id)::uuid,sqlc.arg(subscription_id)::text,sqlc.arg(observed_at)::timestamptz) AS reason
)
SELECT observation.reason::text AS waiting_reason,
 count(*)::bigint AS pending_recipients,count(deadline_at)::bigint AS deadline_recipients,
 count(*) FILTER (WHERE deadline_at<=sqlc.arg(observed_at)::timestamptz)::bigint AS expired_recipients,
 count(*) FILTER (WHERE deadline_at>sqlc.arg(observed_at)::timestamptz AND deadline_at<sqlc.arg(window_end_at)::timestamptz)::bigint AS upcoming_recipients,
 min(deadline_at) FILTER (WHERE isfinite(deadline_at))::timestamptz AS earliest_deadline_at,
 min(deadline_at) FILTER (WHERE deadline_at>sqlc.arg(observed_at)::timestamptz AND deadline_at<sqlc.arg(window_end_at)::timestamptz AND isfinite(deadline_at))::timestamptz AS earliest_upcoming_deadline_at,
 coalesce(bool_or(accepted_at IS NULL OR NOT isfinite(accepted_at) OR accepted_at>sqlc.arg(observed_at)::timestamptz OR (deadline_at IS NOT NULL AND NOT isfinite(deadline_at)) OR (next_attempt_at IS NOT NULL AND NOT isfinite(next_attempt_at)) OR (lease_until IS NOT NULL AND NOT isfinite(lease_until))),false)::boolean AS clock_invalid,
 coalesce(bool_or(lease_until>sqlc.arg(observed_at)::timestamptz),false)::boolean AS active_leases,
 coalesce(bool_or(next_attempt_at>sqlc.arg(observed_at)::timestamptz),false)::boolean AS scheduled_retries
FROM pending CROSS JOIN control CROSS JOIN LATERAL (
 SELECT CASE WHEN deadline_at<=sqlc.arg(observed_at)::timestamptz OR deadline_at<sqlc.arg(window_end_at)::timestamptz THEN
 CASE WHEN NOT (routing_mode='event' AND coalesce(lease_until>sqlc.arg(observed_at)::timestamptz,false)) AND control.reason<>'' THEN control.reason ELSE event_backlog_waiting_reason('pending',capacity_scope,routing_mode,lease_until,next_attempt_at,'application',NULL,sqlc.arg(observed_at)::timestamptz) END ELSE '' END AS reason
) observation GROUP BY observation.reason;
