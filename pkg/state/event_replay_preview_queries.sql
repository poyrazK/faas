-- name: EventReplayPreviewTarget :one
SELECT s.*, a.slug AS app_slug,
       EXISTS (SELECT 1 FROM event_subscription_work_bindings b WHERE b.subscription_id=s.id) AS work_bound
FROM event_subscriptions s JOIN apps a ON a.id=s.app_id AND a.account_id=s.account_id
WHERE s.id=sqlc.arg(subscription_id)::uuid AND s.app_id=sqlc.arg(app_id)::uuid
  AND s.account_id=sqlc.arg(account_id)::uuid AND a.status <> 'deleted';

-- name: EventReplayPreviewEarliestRetained :one
SELECT created_at FROM event_fanout_outbox WHERE account_id=sqlc.arg(account_id)::uuid
ORDER BY created_at, id LIMIT 1;

-- name: EventReplayPreviewCandidates :many
SELECT o.id, o.created_at, o.payload,
       CASE WHEN o.recipient_snapshot IS NULL THEN 'unknown'
            WHEN EXISTS (SELECT 1 FROM jsonb_array_elements(o.recipient_snapshot) s(recipient)
                         WHERE s.recipient->>'id'=sqlc.arg(subscription_id)::text
                           AND s.recipient->>'app_id'=sqlc.arg(app_id)::text)
            THEN 'captured' ELSE 'not_captured' END::text AS original_recipient
FROM event_fanout_outbox o
WHERE o.account_id=sqlc.arg(account_id)::uuid
  AND o.created_at >= sqlc.arg(from_at)::timestamptz AND o.created_at < sqlc.arg(cutoff_at)::timestamptz
  AND (o.created_at,o.id) > (sqlc.arg(after_at)::timestamptz,sqlc.arg(after_id)::bigint)
ORDER BY o.created_at, o.id LIMIT sqlc.arg(page_limit)::integer;

-- name: WorkflowEventReplayPreviewApp :one
SELECT id, slug FROM apps
WHERE id=sqlc.arg(app_id)::uuid AND account_id=sqlc.arg(account_id)::uuid
  AND status <> 'deleted';

-- name: WorkflowEventReplayPreviewCandidates :many
SELECT o.id, o.created_at, o.source, o.event_id, o.event_type,
       coalesce(o.schema_version,'')::text AS schema_version, o.payload,
       (o.recipient_snapshot IS NOT NULL)::boolean AS snapshot_captured,
       target.recipient::jsonb AS recipient,
       coalesce(r.state, o.recipient_progress -> (target.recipient->>'id') ->>'state', 'pending')::text AS routing_state,
       (wer.outbox_id IS NOT NULL)::boolean AS admission_recorded,
       coalesce(wer.run_id::text,'')::text AS workflow_run_id,
       coalesce(wr.status,'')::text AS workflow_run_status
FROM event_fanout_outbox o
LEFT JOIN LATERAL (
    SELECT captured.recipient
    FROM jsonb_array_elements(coalesce(o.recipient_snapshot, '[]'::jsonb)) captured(recipient)
    WHERE captured.recipient->>'app_id'=sqlc.arg(app_id)::text
      AND captured.recipient->'workflow'->>'name'=sqlc.arg(workflow_name)::text
    ORDER BY captured.recipient->>'id'
    LIMIT 1
) target ON true
LEFT JOIN event_fanout_recipients r
  ON r.outbox_id=o.id AND r.subscription_id=target.recipient->>'id'
LEFT JOIN workflow_event_receipts wer
  ON wer.outbox_id=o.id AND wer.recipient_id::text=target.recipient->>'id'
LEFT JOIN workflow_runs wr ON wr.id=wer.run_id
WHERE o.account_id=sqlc.arg(account_id)::uuid
  AND o.created_at >= sqlc.arg(from_at)::timestamptz AND o.created_at < sqlc.arg(cutoff_at)::timestamptz
  AND (o.created_at,o.id) > (sqlc.arg(after_at)::timestamptz,sqlc.arg(after_id)::bigint)
ORDER BY o.created_at, o.id LIMIT sqlc.arg(page_limit)::integer;
