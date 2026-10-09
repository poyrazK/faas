-- name: EventRetentionHealth :one
WITH scoped AS MATERIALIZED (
 SELECT o.id,o.source,o.event_id,o.created_at,o.customer_storage_bytes,
  o.state='delivered' AS settled,
  CASE WHEN o.state='delivered' THEN o.delivered_at + sqlc.arg(retention_seconds)::bigint * interval '1 second' END AS retain_until,
  CASE WHEN o.state='delivered' THEN event_receipt_retention_hold(o.account_id,o.id,o.created_at,sqlc.arg(job_cutoff_at)::timestamptz) ELSE '' END AS hold_reason
 FROM event_fanout_outbox o WHERE o.account_id=sqlc.arg(account_id)::uuid
 AND (sqlc.arg(event_source)::text='' OR o.source=sqlc.arg(event_source)::text)
 AND (sqlc.arg(app_id)::text='' OR
  EXISTS (SELECT 1 FROM jsonb_array_elements(coalesce(o.recipient_snapshot,'[]'::jsonb)) r WHERE r->>'app_id'=sqlc.arg(app_id)::text)
  OR EXISTS (SELECT 1 FROM event_fanout_recipients r WHERE r.outbox_id=o.id AND r.app_id=nullif(sqlc.arg(app_id)::text,'')::uuid))
), sampled AS (
 SELECT source AS event_source,event_id,created_at AS accepted_at,retain_until,customer_storage_bytes AS retained_bytes,hold_reason,
 CASE WHEN hold_reason<>'' THEN 'held' WHEN retain_until<sqlc.arg(now_at)::timestamptz THEN 'eligible_for_pruning' ELSE 'expiring' END AS status
 FROM scoped WHERE settled AND retain_until<=sqlc.arg(window_end)::timestamptz
 ORDER BY retain_until,source,event_id LIMIT sqlc.arg(sample_limit)::integer
)
SELECT count(*)::bigint AS retained_receipts,coalesce(sum(customer_storage_bytes),0)::bigint AS retained_bytes,
 count(*) FILTER (WHERE NOT settled)::bigint AS unsettled_receipts,
 count(*) FILTER (WHERE settled AND retain_until IS NULL)::bigint AS unknown_deadline_receipts,
 count(*) FILTER (WHERE hold_reason<>'')::bigint AS held_receipts,
 count(*) FILTER (WHERE hold_reason='backfill_running')::bigint AS running_backfill_holds,
 count(*) FILTER (WHERE hold_reason='backfill_retryable')::bigint AS retryable_backfill_holds,
 count(*) FILTER (WHERE hold_reason<>'' AND retain_until<sqlc.arg(now_at)::timestamptz)::bigint AS held_due_receipts,
 count(*) FILTER (WHERE settled AND hold_reason='' AND retain_until<sqlc.arg(now_at)::timestamptz)::bigint AS eligible_for_pruning,
 count(*) FILTER (WHERE settled AND hold_reason='' AND retain_until>=sqlc.arg(now_at)::timestamptz AND retain_until<=sqlc.arg(window_end)::timestamptz)::bigint AS expiring_receipts,
 coalesce((SELECT jsonb_agg(to_jsonb(sampled) ORDER BY retain_until,event_source,event_id) FROM sampled),'[]'::jsonb)::jsonb AS sample
FROM scoped;
