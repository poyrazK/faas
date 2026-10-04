-- name: EventReceiptMetadata :one
SELECT o.id, o.account_id, o.source, o.event_id, o.event_type,
       coalesce(o.payload->>'accountid', o.payload->>'account_id', o.account_id::text)::text AS invocation_account_id,
       coalesce(o.schema_version, '')::text AS schema_version, o.created_at, o.delivered_at,
       o.state, o.recipient_claims, (o.recipient_snapshot IS NOT NULL)::boolean AS snapshot_captured,
       jsonb_array_length(coalesce(o.recipient_snapshot, '[]'::jsonb))::integer AS recipient_count,
       coalesce((SELECT jsonb_object_agg(counts.state, counts.n) FROM (
           SELECT coalesce(r.state, o.recipient_progress -> (s.recipient->>'id') ->>'state', 'pending') AS state, count(*) AS n
           FROM jsonb_array_elements(coalesce(o.recipient_snapshot, '[]'::jsonb)) s(recipient)
           LEFT JOIN event_fanout_recipients r ON r.outbox_id=o.id AND r.subscription_id=s.recipient->>'id'
           GROUP BY 1
       ) counts), '{}'::jsonb)::jsonb AS routing_summary
FROM event_fanout_outbox o
WHERE o.account_id=sqlc.arg(account_id)::uuid AND o.source=sqlc.arg(event_source)::text AND o.event_id=sqlc.arg(event_id)::text;

-- name: EventReceiptRecipients :many
SELECT s.position::bigint, s.recipient::jsonb,
       coalesce(a.slug, '')::text AS app_slug, (a.id IS NOT NULL AND a.status <> 'deleted')::boolean AS target_available,
       coalesce(o.recipient_progress -> (s.recipient->>'id'), '{}'::jsonb)::jsonb AS progress,
       coalesce(r.state, o.recipient_progress -> (s.recipient->>'id') ->>'state', 'pending')::text AS routing_state,
       coalesce(r.total_attempts, (o.recipient_progress -> (s.recipient->>'id') ->>'attempts')::integer, 0)::integer AS routing_attempts,
       coalesce(r.generation, 0)::bigint AS generation, coalesce(r.attempts, 0)::integer AS generation_attempts,
       CASE WHEN coalesce(r.state, o.recipient_progress -> (s.recipient->>'id') ->>'state', 'pending')='pending'
            THEN CASE WHEN o.recipient_claims THEN r.available_at ELSE CASE WHEN o.state='pending' THEN o.available_at END END END::timestamptz AS next_attempt_at,
       r.lease_until,
       (SELECT count(*) FROM event_fanout_attempt_history h WHERE h.outbox_id=o.id AND h.subscription_id=s.recipient->>'id' AND h.action='operator_replay')::bigint AS replay_count,
       (SELECT max(h.occurred_at) FROM event_fanout_attempt_history h WHERE h.outbox_id=o.id AND h.subscription_id=s.recipient->>'id' AND h.action='operator_replay')::timestamptz AS last_replayed_at
FROM event_fanout_outbox o
CROSS JOIN LATERAL jsonb_array_elements(coalesce(o.recipient_snapshot, '[]'::jsonb)) WITH ORDINALITY s(recipient, position)
LEFT JOIN event_fanout_recipients r ON r.outbox_id=o.id AND r.subscription_id=s.recipient->>'id'
LEFT JOIN apps a ON a.id=(s.recipient->>'app_id')::uuid AND a.account_id=o.account_id
WHERE o.id=sqlc.arg(outbox_id)::bigint AND o.account_id=sqlc.arg(account_id)::uuid AND s.position > sqlc.arg(after_position)::bigint
ORDER BY s.position LIMIT sqlc.arg(page_limit)::integer;

-- name: EventReceiptInvocations :many
SELECT i.id, i.app_id, i.state, i.attempts, i.replay_generation, coalesce(i.last_error, '')::text AS last_error,
       i.due_at, i.created_at, i.completed_at, coalesce(i.work_policy_name, '')::text AS work_policy_name,
       i.queue_binding_id, i.work_expires_at, i.start_deadline_at,
       EXISTS (SELECT 1 FROM invocation_keyed_replays r WHERE r.parent_invocation_id=i.id) AS keyed_replay_created
FROM invocations i JOIN apps a ON a.id=i.app_id AND a.account_id=i.account_id
WHERE i.account_id=sqlc.arg(account_id)::uuid AND i.id=ANY(sqlc.arg(invocation_ids)::uuid[])
  AND i.created_at >= sqlc.arg(accepted_at)::timestamptz;

-- name: EventReceiptCancellations :many
SELECT c.id, c.app_id, c.cancelled_count, c.created_at
FROM invocation_work_cancellations c JOIN apps a ON a.id=c.app_id
WHERE a.account_id=sqlc.arg(account_id)::uuid AND c.id=ANY(sqlc.arg(invocation_ids)::uuid[])
  AND c.created_at >= sqlc.arg(accepted_at)::timestamptz;

-- name: EventReceiptAcceptedAt :one
SELECT created_at FROM event_fanout_outbox
WHERE account_id=sqlc.arg(account_id)::uuid AND source=sqlc.arg(event_source)::text AND event_id=sqlc.arg(event_id)::text;

-- name: EventReceiptReplaySummaries :many
SELECT roots.id::uuid AS root_id, latest.* FROM unnest(sqlc.arg(invocation_ids)::uuid[]) roots(id)
CROSS JOIN LATERAL (
  SELECT i.id, i.app_id, i.state, i.attempts, i.replay_generation, i.replayed_from_invocation_id,
         coalesce(i.last_error, '')::text AS last_error, i.due_at, i.created_at, i.completed_at,
         coalesce(i.work_policy_name, '')::text AS work_policy_name, i.queue_binding_id, i.work_expires_at, i.start_deadline_at,
         EXISTS (SELECT 1 FROM invocation_keyed_replays r WHERE r.parent_invocation_id=i.id) AS keyed_replay_created,
         count(*) OVER ()::bigint AS retained_replay_count
  FROM invocations i JOIN apps a ON a.id=i.app_id AND a.account_id=i.account_id
  WHERE i.account_id=sqlc.arg(account_id)::uuid AND i.replay_root_invocation_id=roots.id
    AND i.replay_root_created_at >= sqlc.arg(accepted_at)::timestamptz
  ORDER BY i.created_at DESC, i.id DESC LIMIT 1
) latest;

-- name: EventReceiptReplayTarget :one
SELECT a.id AS app_id FROM event_fanout_outbox o
CROSS JOIN LATERAL jsonb_array_elements(coalesce(o.recipient_snapshot, '[]'::jsonb)) s(recipient)
JOIN apps a ON a.id=(s.recipient->>'app_id')::uuid AND a.account_id=o.account_id
WHERE o.id=sqlc.arg(outbox_id)::bigint AND o.account_id=sqlc.arg(account_id)::uuid
  AND s.recipient->>'id'=sqlc.arg(subscription_id)::text;

-- name: EventReceiptReplayHistory :many
SELECT i.id, i.state, i.attempts, i.replay_generation, i.replayed_from_invocation_id,
       coalesce(i.last_error, '')::text AS last_error, i.due_at, i.created_at, i.completed_at
FROM invocations i JOIN apps a ON a.id=i.app_id AND a.account_id=i.account_id
WHERE i.account_id=sqlc.arg(account_id)::uuid AND i.app_id=sqlc.arg(app_id)::uuid
  AND i.replay_root_invocation_id=sqlc.arg(root_invocation_id)::uuid
  AND i.replay_root_created_at >= sqlc.arg(accepted_at)::timestamptz
  AND (sqlc.narg(after_created_at)::timestamptz IS NULL OR
       (i.created_at, i.id)<(sqlc.narg(after_created_at)::timestamptz, sqlc.narg(after_id)::uuid))
ORDER BY i.created_at DESC, i.id DESC LIMIT sqlc.arg(page_limit)::integer;
