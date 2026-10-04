-- name: EventRecipientInitializeReceipt :execrows
UPDATE event_fanout_outbox
SET recipient_claims = true, claim_token = NULL, lease_until = NULL
WHERE id = sqlc.arg(id)::bigint AND NOT recipient_claims
  AND state = 'processing' AND claim_token = sqlc.arg(claim_token)::uuid
  AND lease_until > clock_timestamp() AND recipient_snapshot IS NOT NULL;

-- name: EventRecipientInsert :exec
INSERT INTO event_fanout_recipients
    (outbox_id, subscription_id, app_id, recipient, state, attempts, total_attempts, available_at)
VALUES (sqlc.arg(outbox_id)::bigint, sqlc.arg(subscription_id)::text, sqlc.arg(app_id)::uuid,
        sqlc.arg(recipient)::jsonb, sqlc.arg(state)::text, 0,
        sqlc.arg(total_attempts)::integer, sqlc.arg(available_at)::timestamptz)
ON CONFLICT (outbox_id, subscription_id) DO NOTHING;

-- name: EventRecipientClaim :one
WITH candidate AS (
    SELECT r.outbox_id, r.subscription_id
    FROM event_fanout_recipients r
    WHERE (r.state = 'pending' AND r.available_at <= sqlc.arg(now_at)::timestamptz)
       OR (r.state = 'processing' AND r.lease_until <= sqlc.arg(now_at)::timestamptz)
    ORDER BY r.available_at, r.outbox_id, r.subscription_id
    FOR UPDATE SKIP LOCKED LIMIT 1
), claimed AS (
    UPDATE event_fanout_recipients r
    SET state = 'processing', claim_token = gen_random_uuid(),
        lease_until = sqlc.arg(now_at)::timestamptz + interval '5 minutes',
        attempts = r.attempts + 1, total_attempts = r.total_attempts + 1
    FROM candidate c
    WHERE r.outbox_id = c.outbox_id AND r.subscription_id = c.subscription_id
    RETURNING r.*
)
SELECT c.outbox_id, c.recipient, c.claim_token, c.generation, c.attempts,
       c.total_attempts, c.available_at, c.lease_until, o.payload
FROM claimed c JOIN event_fanout_outbox o ON o.id = c.outbox_id;

-- name: EventRecipientLockReceipt :one
SELECT id FROM event_fanout_outbox
WHERE id = sqlc.arg(id)::bigint AND recipient_claims FOR UPDATE;

-- name: EventRecipientFinish :execrows
UPDATE event_fanout_recipients
SET state = sqlc.arg(state)::text, available_at = sqlc.arg(available_at)::timestamptz,
    claim_token = NULL, lease_until = NULL
WHERE outbox_id = sqlc.arg(outbox_id)::bigint AND subscription_id = sqlc.arg(subscription_id)::text
  AND state = 'processing' AND claim_token = sqlc.arg(claim_token)::uuid
  AND generation = sqlc.arg(generation)::bigint AND lease_until > clock_timestamp();

-- name: EventRecipientUpdateProgress :exec
UPDATE event_fanout_outbox o
SET recipient_progress = jsonb_set(o.recipient_progress, ARRAY[sqlc.arg(subscription_id)::text], sqlc.arg(progress)::jsonb, true),
    last_error = (SELECT left('subscription ' || p.key || ': ' || coalesce(p.value->>'last_error', 'recipient failed'), 1024)
        FROM jsonb_each(jsonb_set(o.recipient_progress, ARRAY[sqlc.arg(subscription_id)::text], sqlc.arg(progress)::jsonb, true)) p
        WHERE p.value->>'state' = 'failed' ORDER BY p.key LIMIT 1)
WHERE o.id = sqlc.arg(id)::bigint;

-- name: EventRecipientAppendHistory :exec
INSERT INTO event_fanout_attempt_history
    (outbox_id, app_id, subscription_id, action, state, attempts, failure_code, retryable, last_error, occurred_at)
VALUES (sqlc.arg(outbox_id)::bigint, sqlc.arg(app_id)::uuid, sqlc.arg(subscription_id)::text,
        sqlc.arg(action)::text, sqlc.arg(state)::text, sqlc.arg(attempts)::integer,
        sqlc.arg(failure_code)::text, sqlc.arg(retryable)::boolean, sqlc.arg(last_error)::text,
        sqlc.arg(occurred_at)::timestamptz);

-- name: EventRecipientSettleReceipt :exec
UPDATE event_fanout_outbox o
SET state = CASE WHEN EXISTS (SELECT 1 FROM event_fanout_recipients r
        WHERE r.outbox_id = o.id AND r.state IN ('pending', 'processing')) THEN 'processing' ELSE 'delivered' END,
    delivered_at = CASE WHEN EXISTS (SELECT 1 FROM event_fanout_recipients r
        WHERE r.outbox_id = o.id AND r.state IN ('pending', 'processing')) THEN NULL ELSE now() END
WHERE o.id = sqlc.arg(id)::bigint AND o.recipient_claims;

-- name: EventRecipientReplayReceipt :one
SELECT o.id, o.recipient_claims,
       (o.recipient_progress -> sqlc.arg(subscription_id)::text)::jsonb AS progress
FROM event_fanout_outbox o
CROSS JOIN LATERAL jsonb_array_elements(COALESCE(o.recipient_snapshot, '[]'::jsonb)) r(recipient)
WHERE o.account_id = sqlc.arg(account_id)::uuid AND o.source = sqlc.arg(event_source)::text
  AND o.event_id = sqlc.arg(event_id)::text AND r.recipient->>'app_id' = sqlc.arg(app_id)::text
  AND r.recipient->>'id' = sqlc.arg(subscription_id)::text
FOR UPDATE OF o;

-- name: EventRecipientReplay :execrows
UPDATE event_fanout_recipients
SET state = 'pending', generation = generation + 1, attempts = 0,
    available_at = sqlc.arg(now_at)::timestamptz, claim_token = NULL, lease_until = NULL
WHERE outbox_id = sqlc.arg(outbox_id)::bigint AND subscription_id = sqlc.arg(subscription_id)::text
  AND state = 'failed';

-- name: EventRecipientReplayCandidates :many
SELECT o.id, o.recipient_claims, (r.recipient->>'id')::text AS subscription_id,
       (o.recipient_progress -> (r.recipient->>'id'))::jsonb AS progress
FROM event_fanout_outbox o
JOIN apps a ON a.id = sqlc.arg(app_id)::uuid AND a.account_id = o.account_id
CROSS JOIN LATERAL jsonb_array_elements(COALESCE(o.recipient_snapshot, '[]'::jsonb)) r(recipient)
WHERE o.account_id = sqlc.arg(account_id)::uuid AND r.recipient->>'app_id' = a.id::text
  AND (o.recipient_claims OR o.state IN ('delivered', 'pending'))
  AND (o.recipient_progress -> (r.recipient->>'id'))->>'state' = 'failed'
  AND coalesce(((o.recipient_progress -> (r.recipient->>'id'))->>'retryable')::boolean, false)
  AND ((sqlc.arg(event_source)::text = '' AND sqlc.arg(event_id)::text = '') OR
       (o.source = sqlc.arg(event_source)::text AND o.event_id = sqlc.arg(event_id)::text))
ORDER BY coalesce((o.recipient_progress -> (r.recipient->>'id')->>'updated_at')::timestamptz, o.created_at), o.id, r.recipient->>'id'
LIMIT sqlc.arg(page_limit)::integer FOR UPDATE OF o SKIP LOCKED;

-- name: EventRecipientReplayLegacy :execrows
UPDATE event_fanout_outbox
SET state = 'pending', available_at = sqlc.arg(now_at)::timestamptz,
    delivered_at = NULL, claim_token = NULL, lease_until = NULL
WHERE id = sqlc.arg(id)::bigint AND NOT recipient_claims AND state IN ('delivered', 'pending')
  AND (recipient_progress -> sqlc.arg(subscription_id)::text)->>'state' = 'failed';

-- name: EventRecipientReplayHasMore :one
SELECT EXISTS (
    SELECT 1 FROM event_fanout_outbox o
    CROSS JOIN LATERAL jsonb_array_elements(COALESCE(o.recipient_snapshot, '[]'::jsonb)) r(recipient)
    WHERE o.account_id = sqlc.arg(account_id)::uuid AND r.recipient->>'app_id' = sqlc.arg(app_id)::text
      AND (o.recipient_progress -> (r.recipient->>'id'))->>'state' = 'failed'
      AND coalesce(((o.recipient_progress -> (r.recipient->>'id'))->>'retryable')::boolean, false)
      AND ((sqlc.arg(event_source)::text = '' AND sqlc.arg(event_id)::text = '') OR
           (o.source = sqlc.arg(event_source)::text AND o.event_id = sqlc.arg(event_id)::text))
) AS has_more;
