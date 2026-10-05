-- name: EventRecipientInitializeReceipt :execrows
UPDATE event_fanout_outbox
SET recipient_claims = true, claim_token = NULL, lease_until = NULL
WHERE id = sqlc.arg(id)::bigint AND NOT recipient_claims
  AND state = 'processing' AND claim_token = sqlc.arg(claim_token)::uuid
  AND lease_until > clock_timestamp() AND recipient_snapshot IS NOT NULL;

-- name: EventRoutingReceipt :one
SELECT * FROM event_fanout_outbox WHERE id=sqlc.arg(id)::bigint;

-- name: EventRoutingLockReceipt :one
SELECT * FROM event_fanout_outbox WHERE id=sqlc.arg(id)::bigint FOR UPDATE;

-- name: EventRoutingLockRecipient :one
SELECT * FROM event_fanout_recipients
WHERE outbox_id=sqlc.arg(outbox_id)::bigint AND subscription_id=sqlc.arg(subscription_id)::text FOR UPDATE;

-- name: EventRoutingLockApp :one
SELECT id FROM apps WHERE id=sqlc.arg(app_id)::uuid AND account_id=sqlc.arg(account_id)::uuid
    AND status <> 'deleted' FOR SHARE;

-- name: EventRoutingClaimValid :one
-- Evaluate the wall clock only after all admission locks have been acquired.
SELECT CASE WHEN o.recipient_claims THEN EXISTS (
    SELECT 1 FROM event_fanout_recipients r WHERE r.outbox_id=o.id
      AND r.subscription_id=sqlc.arg(subscription_id)::text AND r.state='processing'
      AND r.generation=sqlc.arg(generation)::bigint AND r.claim_token=sqlc.arg(claim_token)::uuid
      AND r.lease_until>clock_timestamp())
    ELSE sqlc.arg(generation)::bigint=0 AND o.state='processing'
      AND o.claim_token=sqlc.arg(claim_token)::uuid AND o.lease_until>clock_timestamp() END::boolean AS valid
FROM event_fanout_outbox o WHERE o.id=sqlc.arg(id)::bigint;

-- name: EventRoutingSettleSnapshot :one
UPDATE event_fanout_outbox o SET state='delivered',delivered_at=clock_timestamp(),claim_token=NULL,lease_until=NULL
WHERE o.id=sqlc.arg(id)::bigint AND NOT o.recipient_claims
  AND NOT EXISTS (SELECT 1 FROM jsonb_array_elements(o.recipient_snapshot) r
    WHERE coalesce(o.recipient_progress -> (r->>'id')->>'state','pending') NOT IN ('enqueued','filtered','failed'))
RETURNING id;

-- name: EventRoutingRecordProgress :one
-- An uncertain commit response must not overwrite its durable admission proof.
UPDATE event_fanout_outbox o
SET recipient_progress=jsonb_set(o.recipient_progress,ARRAY[sqlc.arg(subscription_id)::text],sqlc.arg(progress)::jsonb,true),
    last_error=CASE WHEN sqlc.arg(progress)::jsonb->>'state'='failed'
      THEN left('subscription ' || sqlc.arg(subscription_id)::text || ': ' || coalesce(sqlc.arg(progress)::jsonb->>'last_error','recipient failed'),1024)
      ELSE o.last_error END
WHERE o.id=sqlc.arg(id)::bigint AND o.claim_token=sqlc.arg(claim_token)::uuid
    AND o.state='processing' AND NOT o.recipient_claims AND o.lease_until>clock_timestamp()
    AND coalesce(o.recipient_progress->sqlc.arg(subscription_id)::text->>'state','pending') NOT IN ('enqueued','filtered')
    AND coalesce((o.recipient_progress->sqlc.arg(subscription_id)::text->>'capacity_deferrals')::integer,0)
      <=coalesce((sqlc.arg(progress)::jsonb->>'capacity_deferrals')::integer,0)
    AND EXISTS (SELECT 1 FROM jsonb_array_elements(o.recipient_snapshot) r WHERE r->>'id'=sqlc.arg(subscription_id)::text)
RETURNING (SELECT r->>'app_id' FROM jsonb_array_elements(o.recipient_snapshot) r WHERE r->>'id'=sqlc.arg(subscription_id)::text LIMIT 1)::text AS app_id;

-- name: EventRecipientInsert :exec
INSERT INTO event_fanout_recipients
    (outbox_id, subscription_id, app_id, recipient, state, attempts, total_attempts, capacity_deferrals, available_at)
VALUES (sqlc.arg(outbox_id)::bigint, sqlc.arg(subscription_id)::text, sqlc.arg(app_id)::uuid,
        sqlc.arg(recipient)::jsonb, sqlc.arg(state)::text, 0,
        sqlc.arg(total_attempts)::integer, sqlc.arg(capacity_deferrals)::integer, sqlc.arg(available_at)::timestamptz)
ON CONFLICT (outbox_id, subscription_id) DO NOTHING;

-- name: EventRecipientClaim :one
WITH candidate AS (
    SELECT r.outbox_id, r.subscription_id
    FROM event_fanout_recipients r
    JOIN event_fanout_outbox o ON o.id=r.outbox_id
    LEFT JOIN event_routing_fairness fa ON fa.account_id=o.account_id AND fa.subscription_id=''
    LEFT JOIN event_routing_fairness fc ON fc.account_id=o.account_id AND fc.subscription_id=r.subscription_id
    WHERE (r.state = 'pending' AND r.available_at <= sqlc.arg(now_at)::timestamptz)
       OR (r.state = 'processing' AND r.lease_until <= sqlc.arg(now_at)::timestamptz)
    ORDER BY coalesce(fa.last_claimed_at,'epoch'::timestamptz),
      coalesce(fc.last_claimed_at,'epoch'::timestamptz), r.available_at, r.outbox_id, r.subscription_id
    FOR UPDATE OF r SKIP LOCKED LIMIT 1
), claimed AS (
    UPDATE event_fanout_recipients r
    SET state = 'processing', claim_token = gen_random_uuid(),
        lease_until = sqlc.arg(now_at)::timestamptz + interval '5 minutes',
        attempts = r.attempts + 1, total_attempts = r.total_attempts + 1
    FROM candidate c
    WHERE r.outbox_id = c.outbox_id AND r.subscription_id = c.subscription_id
    RETURNING r.*
), fairness AS (
 INSERT INTO event_routing_fairness
 SELECT o.account_id,v.subscription_id,clock_timestamp() FROM claimed c
 JOIN event_fanout_outbox o ON o.id=c.outbox_id
 CROSS JOIN LATERAL (VALUES (''::text),(c.subscription_id)) v(subscription_id)
 ORDER BY o.account_id,v.subscription_id
 ON CONFLICT (account_id,subscription_id) DO UPDATE SET last_claimed_at=excluded.last_claimed_at
 RETURNING account_id
)
SELECT c.outbox_id, c.recipient, c.claim_token, c.generation, c.attempts,
       c.capacity_deferrals,c.generation_capacity_deferrals,
       c.total_attempts, c.available_at, c.lease_until, o.payload
FROM claimed c JOIN event_fanout_outbox o ON o.id = c.outbox_id;

-- name: EventRecipientLockReceipt :one
SELECT id FROM event_fanout_outbox
WHERE id = sqlc.arg(id)::bigint AND recipient_claims FOR UPDATE;

-- name: EventRecipientFinish :execrows
UPDATE event_fanout_recipients
SET state = sqlc.arg(state)::text, available_at = sqlc.arg(available_at)::timestamptz,
    claim_token = NULL, lease_until = NULL,
    capacity_deferrals=capacity_deferrals+CASE WHEN sqlc.arg(capacity_deferred)::boolean THEN 1 ELSE 0 END,
    generation_capacity_deferrals=generation_capacity_deferrals+CASE WHEN sqlc.arg(capacity_deferred)::boolean THEN 1 ELSE 0 END
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
SET state = 'pending', generation = generation + 1, attempts = 0, generation_capacity_deferrals=0,
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

-- name: EventDeliveryLockCapacity :exec
INSERT INTO event_delivery_capacity (account_id,consumer_limit,app_limit,account_limit)
VALUES (sqlc.arg(account_id)::uuid,sqlc.arg(consumer_limit)::integer,sqlc.arg(app_limit)::integer,sqlc.arg(account_limit)::integer)
ON CONFLICT (account_id) DO UPDATE SET consumer_limit=excluded.consumer_limit,app_limit=excluded.app_limit,account_limit=excluded.account_limit;

-- name: EventDeliveryCounts :one
SELECT count(*)::bigint AS account_count,
 count(*) FILTER (WHERE s.app_id=sqlc.arg(app_id)::uuid)::bigint AS app_count,
 count(*) FILTER (WHERE s.app_id=sqlc.arg(app_id)::uuid AND s.subscription_id=sqlc.arg(subscription_id)::text)::bigint AS consumer_count
FROM event_delivery_slots s JOIN invocations i ON i.id=s.invocation_id
WHERE s.account_id=sqlc.arg(account_id)::uuid AND i.state IN ('pending','dispatching')
 AND NOT (sqlc.arg(replace_pending)::boolean AND i.state='pending' AND i.app_id=sqlc.arg(app_id)::uuid
   AND coalesce(i.work_policy_name,'')=sqlc.arg(policy_name)::text AND i.work_key_digest=sqlc.arg(key_digest)::bytea);

-- name: EventDeliveryInsertSlot :exec
INSERT INTO event_delivery_slots (invocation_id,account_id,app_id,subscription_id)
VALUES (sqlc.arg(invocation_id)::uuid,sqlc.arg(account_id)::uuid,sqlc.arg(app_id)::uuid,sqlc.arg(subscription_id)::text)
ON CONFLICT DO NOTHING;

-- name: EventRoutingHealth :one
SELECT count(*) FILTER (WHERE coalesce(p.value->>'capacity_scope','')<>'')::bigint AS capacity_waiting,
 coalesce(extract(epoch FROM clock_timestamp()-min(o.created_at)),0)::double precision AS oldest_pending_seconds
FROM event_fanout_outbox o
CROSS JOIN LATERAL jsonb_array_elements(coalesce(o.recipient_snapshot,'[]'::jsonb)) r
CROSS JOIN LATERAL (SELECT coalesce(o.recipient_progress->(r->>'id'),'{}'::jsonb) AS value) p
WHERE o.state<>'delivered' AND coalesce(p.value->>'state','pending') IN ('pending','processing');

-- name: EventRoutingClaimReceipt :one
WITH candidate AS (
 SELECT o.id FROM event_fanout_outbox o
 LEFT JOIN event_routing_fairness fa ON fa.account_id=o.account_id AND fa.subscription_id=''
 LEFT JOIN LATERAL (
   SELECT min(coalesce(fc.last_claimed_at,'epoch'::timestamptz)) AS last_claimed_at
   FROM jsonb_array_elements(coalesce(o.recipient_snapshot,'[]'::jsonb)) r
   LEFT JOIN event_routing_fairness fc ON fc.account_id=o.account_id AND fc.subscription_id=r->>'id'
   WHERE coalesce(o.recipient_progress->(r->>'id')->>'state','pending') NOT IN ('enqueued','filtered','failed')
     AND coalesce((o.recipient_progress->(r->>'id')->>'next_attempt_at')::timestamptz,'epoch'::timestamptz)<=sqlc.arg(now_at)::timestamptz
 ) cf ON true
 WHERE NOT o.recipient_claims AND ((o.state='pending' AND o.available_at<=sqlc.arg(now_at)::timestamptz)
   OR (o.state='processing' AND o.lease_until<=sqlc.arg(now_at)::timestamptz))
 ORDER BY coalesce(fa.last_claimed_at,'epoch'::timestamptz),
   coalesce(cf.last_claimed_at,fa.last_claimed_at,'epoch'::timestamptz),o.id
 FOR UPDATE OF o SKIP LOCKED LIMIT 1
), claimed AS (
 UPDATE event_fanout_outbox o SET state='processing',claim_token=gen_random_uuid(),
 lease_until=sqlc.arg(now_at)::timestamptz+interval '5 minutes',attempts=o.attempts+1
 FROM candidate c WHERE o.id=c.id RETURNING o.*
), fairness AS (
 INSERT INTO event_routing_fairness
 SELECT c.account_id,v.subscription_id,clock_timestamp() FROM claimed c
 CROSS JOIN LATERAL (
   SELECT ''::text AS subscription_id UNION
   SELECT r->>'id' FROM jsonb_array_elements(coalesce(c.recipient_snapshot,'[]'::jsonb)) r
   WHERE coalesce(c.recipient_progress->(r->>'id')->>'state','pending') NOT IN ('enqueued','filtered','failed')
     AND coalesce((c.recipient_progress->(r->>'id')->>'next_attempt_at')::timestamptz,'epoch'::timestamptz)<=sqlc.arg(now_at)::timestamptz
 ) v
 ORDER BY c.account_id,v.subscription_id
 ON CONFLICT (account_id,subscription_id) DO UPDATE SET last_claimed_at=excluded.last_claimed_at RETURNING account_id
)
SELECT c.* FROM claimed c WHERE EXISTS (SELECT 1 FROM fairness f WHERE f.account_id=c.account_id);

-- name: EventDeliveryReplayAccount :one
SELECT s.account_id,a.plan FROM event_delivery_slots s JOIN accounts a ON a.id=s.account_id
WHERE s.invocation_id=sqlc.arg(invocation_id)::uuid;

-- name: EventRoutingDeferReceipt :execrows
UPDATE event_fanout_outbox o SET state='pending',claim_token=NULL,lease_until=NULL,
 available_at=coalesce((SELECT min(coalesce((p.value->>'next_attempt_at')::timestamptz,
 clock_timestamp()+interval '5 seconds')) FROM jsonb_each(o.recipient_progress) p WHERE p.value->>'state'='pending'),clock_timestamp()+interval '5 seconds')
WHERE o.id=sqlc.arg(id)::bigint AND o.claim_token=sqlc.arg(claim_token)::uuid
 AND o.state='processing' AND NOT o.recipient_claims AND o.lease_until>clock_timestamp();
