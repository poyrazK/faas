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
    (outbox_id, subscription_id, app_id, recipient, state, attempts, total_attempts, capacity_deferrals, available_at, delivery_deadline_at)
VALUES (sqlc.arg(outbox_id)::bigint, sqlc.arg(subscription_id)::text, sqlc.arg(app_id)::uuid,
        sqlc.arg(recipient)::jsonb, sqlc.arg(state)::text, 0,
        sqlc.arg(total_attempts)::integer, sqlc.arg(capacity_deferrals)::integer, sqlc.arg(available_at)::timestamptz, (SELECT event_recipient_delivery_deadline(sqlc.arg(recipient)::jsonb,o.created_at,o.recipient_progress->sqlc.arg(subscription_id)::text) FROM event_fanout_outbox o WHERE o.id=sqlc.arg(outbox_id)::bigint))
ON CONFLICT (outbox_id, subscription_id) DO NOTHING;

-- name: EventRecipientClaim :one
WITH candidate AS (
    SELECT r.outbox_id, r.subscription_id
    FROM event_fanout_recipients r
    JOIN event_fanout_outbox o ON o.id=r.outbox_id
    LEFT JOIN event_routing_fairness fa ON fa.account_id=o.account_id AND fa.subscription_id=''
    LEFT JOIN event_routing_fairness fc ON fc.account_id=o.account_id AND fc.subscription_id=r.subscription_id
    WHERE ((r.state = 'pending' AND (r.available_at <= sqlc.arg(now_at)::timestamptz OR r.delivery_deadline_at <= sqlc.arg(now_at)::timestamptz OR event_recipient_schema_version_mismatch(r.recipient,o.payload)))
       OR (r.state = 'processing' AND r.lease_until <= sqlc.arg(now_at)::timestamptz))
      AND (sqlc.arg(include_workflows)::boolean OR NOT r.recipient ? 'workflow')
      AND (r.backfill_job_id IS NULL OR EXISTS (
          SELECT 1 FROM event_replay_jobs j JOIN event_replay_job_items i ON i.job_id=j.id
          WHERE j.id=r.backfill_job_id AND j.state='running' AND i.outbox_id=r.outbox_id
            AND i.state IN ('pending','processing')))
      AND (r.delivery_deadline_at <= sqlc.arg(now_at)::timestamptz OR event_recipient_schema_version_mismatch(r.recipient,o.payload) OR event_subscription_delivery_waiting_reason(o.account_id,r.app_id,r.subscription_id,sqlc.arg(now_at)::timestamptz)='')
      AND (r.delivery_deadline_at <= sqlc.arg(now_at)::timestamptz OR event_recipient_schema_version_mismatch(r.recipient,o.payload) OR NOT event_recipient_order_blocked(r.outbox_id, r.subscription_id, r.recipient, true))
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
), replay_item AS (
    UPDATE event_replay_job_items i SET state='processing', attempts=c.total_attempts, updated_at=clock_timestamp()
    FROM claimed c WHERE c.backfill_job_id=i.job_id AND c.outbox_id=i.outbox_id
      AND i.state IN ('pending','processing')
    RETURNING i.job_id
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
       c.total_attempts, c.available_at, c.lease_until, o.payload, o.created_at AS accepted_at, c.backfill_job_id, (o.recipient_progress->c.subscription_id)::jsonb AS previous_progress
FROM claimed c JOIN event_fanout_outbox o ON o.id = c.outbox_id;

-- name: EventRecipientClaimBatch :many
-- One scan claims what back-to-back EventRecipientClaim calls would claim, in
-- the same order: each round takes one recipient from every eligible account
-- in service order and rotates the account's consumers. A consumer with
-- delivery controls contributes one recipient per batch, so its admission
-- gate sees current counters before its next claim.
WITH eligible AS MATERIALIZED (
    SELECT r.outbox_id, r.subscription_id, o.account_id, r.available_at,
        event_recipient_schema_version_mismatch(r.recipient,o.payload) AS schema_mismatch,
        coalesce(fa.last_claimed_at,'epoch'::timestamptz) AS account_served,
        coalesce(fc.last_claimed_at,'epoch'::timestamptz) AS consumer_served,
        (EXISTS (SELECT 1 FROM event_subscription_delivery_controls c
            WHERE c.app_id=r.app_id AND c.account_id=o.account_id AND c.subscription_id::text=r.subscription_id)
         OR EXISTS (SELECT 1 FROM event_subscription_circuit_breakers b
            WHERE b.app_id=r.app_id AND b.account_id=o.account_id AND b.subscription_id::text=r.subscription_id)) AS controlled
    FROM event_fanout_recipients r
    JOIN event_fanout_outbox o ON o.id=r.outbox_id
    LEFT JOIN event_routing_fairness fa ON fa.account_id=o.account_id AND fa.subscription_id=''
    LEFT JOIN event_routing_fairness fc ON fc.account_id=o.account_id AND fc.subscription_id=r.subscription_id
    WHERE ((r.state = 'pending' AND (r.available_at <= sqlc.arg(now_at)::timestamptz OR r.delivery_deadline_at <= sqlc.arg(now_at)::timestamptz OR event_recipient_schema_version_mismatch(r.recipient,o.payload)))
       OR (r.state = 'processing' AND r.lease_until <= sqlc.arg(now_at)::timestamptz))
      AND (sqlc.arg(include_workflows)::boolean OR NOT r.recipient ? 'workflow')
      AND (r.backfill_job_id IS NULL OR EXISTS (
          SELECT 1 FROM event_replay_jobs j JOIN event_replay_job_items i ON i.job_id=j.id
          WHERE j.id=r.backfill_job_id AND j.state='running' AND i.outbox_id=r.outbox_id
            AND i.state IN ('pending','processing')))
      AND (r.delivery_deadline_at <= sqlc.arg(now_at)::timestamptz OR event_recipient_schema_version_mismatch(r.recipient,o.payload) OR event_subscription_delivery_waiting_reason(o.account_id,r.app_id,r.subscription_id,sqlc.arg(now_at)::timestamptz)='')
      AND (r.delivery_deadline_at <= sqlc.arg(now_at)::timestamptz OR event_recipient_schema_version_mismatch(r.recipient,o.payload) OR NOT event_recipient_order_blocked(r.outbox_id, r.subscription_id, r.recipient, true))
), consumer_rounds AS (
    SELECT e.*, row_number() OVER consumer_order AS consumer_round,
        first_value(e.available_at) OVER consumer_order AS consumer_first_available_at,
        first_value(e.outbox_id) OVER consumer_order AS consumer_first_outbox_id
    FROM eligible e
    WINDOW consumer_order AS (PARTITION BY e.account_id, e.subscription_id ORDER BY e.available_at, e.outbox_id)
), account_rounds AS (
    SELECT c.*, row_number() OVER (PARTITION BY c.account_id ORDER BY c.consumer_round, c.consumer_served,
        c.consumer_first_available_at, c.consumer_first_outbox_id, c.subscription_id) AS account_round
    FROM consumer_rounds c
    WHERE c.consumer_round = 1 OR NOT c.controlled
), account_firsts AS (
    -- Later rounds visit accounts in the order their first claims took.
    SELECT a.*, first_value(a.consumer_served) OVER account_order AS first_consumer_served,
        first_value(a.available_at) OVER account_order AS first_available_at,
        first_value(a.outbox_id) OVER account_order AS first_outbox_id,
        first_value(a.subscription_id) OVER account_order AS first_subscription_id
    FROM account_rounds a
    WINDOW account_order AS (PARTITION BY a.account_id ORDER BY a.account_round)
), ordered AS (
    SELECT f.outbox_id, f.subscription_id, f.schema_mismatch,
        row_number() OVER (ORDER BY f.account_round, f.account_served, f.first_consumer_served,
            f.first_available_at, f.first_outbox_id, f.first_subscription_id) AS position
    FROM account_firsts f
), candidate AS (
    -- Repeating the due test here re-checks a row another worker claimed
    -- after this statement's snapshot, before SKIP LOCKED could skip it.
    -- Only the first ranks can be claimed, so only they are looked up; a
    -- batch short of rows that other workers hold is topped up next scan.
    SELECT r.outbox_id, r.subscription_id, k.position
    FROM event_fanout_recipients r
    JOIN ordered k ON k.outbox_id=r.outbox_id AND k.subscription_id=r.subscription_id
    WHERE k.position <= 2 * sqlc.arg(batch_limit)::integer
      AND ((r.state = 'pending' AND (r.available_at <= sqlc.arg(now_at)::timestamptz OR r.delivery_deadline_at <= sqlc.arg(now_at)::timestamptz OR k.schema_mismatch))
       OR (r.state = 'processing' AND r.lease_until <= sqlc.arg(now_at)::timestamptz))
    ORDER BY k.position
    FOR UPDATE OF r SKIP LOCKED LIMIT sqlc.arg(batch_limit)::integer
), claimed AS (
    UPDATE event_fanout_recipients r
    SET state = 'processing', claim_token = gen_random_uuid(),
        lease_until = sqlc.arg(now_at)::timestamptz + interval '5 minutes',
        attempts = r.attempts + 1, total_attempts = r.total_attempts + 1
    FROM candidate c
    WHERE r.outbox_id = c.outbox_id AND r.subscription_id = c.subscription_id
    RETURNING r.*, c.position
), replay_item AS (
    UPDATE event_replay_job_items i SET state='processing', attempts=c.total_attempts, updated_at=clock_timestamp()
    FROM claimed c WHERE c.backfill_job_id=i.job_id AND c.outbox_id=i.outbox_id
      AND i.state IN ('pending','processing')
    RETURNING i.job_id
), stamp AS MATERIALIZED (
    SELECT clock_timestamp() AS at
), served AS (
    -- Each account and consumer is stamped at its last claim in the batch, as
    -- back-to-back claims would leave it.
    SELECT o.account_id, ''::text AS subscription_id, max(c.position) AS position
    FROM claimed c JOIN event_fanout_outbox o ON o.id=c.outbox_id
    GROUP BY o.account_id
    UNION ALL
    SELECT o.account_id, c.subscription_id, max(c.position)
    FROM claimed c JOIN event_fanout_outbox o ON o.id=c.outbox_id
    GROUP BY o.account_id, c.subscription_id
), fairness AS (
    -- Rows are written in key order, the lock order every claim uses.
    INSERT INTO event_routing_fairness
    SELECT s.account_id, s.subscription_id, t.at + s.position * interval '1 microsecond' FROM served s CROSS JOIN stamp t
    ORDER BY s.account_id, s.subscription_id
    ON CONFLICT (account_id,subscription_id) DO UPDATE SET last_claimed_at=excluded.last_claimed_at
    RETURNING account_id
)
SELECT c.outbox_id, c.recipient, c.claim_token, c.generation, c.attempts,
       c.capacity_deferrals,c.generation_capacity_deferrals,
       c.total_attempts, c.available_at, c.lease_until, o.payload, o.created_at AS accepted_at, c.backfill_job_id, (o.recipient_progress->c.subscription_id)::jsonb AS previous_progress
FROM claimed c JOIN event_fanout_outbox o ON o.id = c.outbox_id
ORDER BY c.position;

-- name: EventRecipientReleaseClaim :execrows
-- Returns an unprocessed batch claim as if it had not been made.
UPDATE event_fanout_recipients
SET state = 'pending', claim_token = NULL, lease_until = NULL,
    attempts = attempts - 1, total_attempts = total_attempts - 1
WHERE outbox_id = sqlc.arg(outbox_id)::bigint AND subscription_id = sqlc.arg(subscription_id)::text
  AND state = 'processing' AND claim_token = sqlc.arg(claim_token)::uuid
  AND generation = sqlc.arg(generation)::bigint;

-- name: EventRecipientLockReceipt :one
SELECT id FROM event_fanout_outbox
WHERE id = sqlc.arg(id)::bigint AND recipient_claims FOR UPDATE;

-- name: EventRecipientFinish :execrows
UPDATE event_fanout_recipients
SET attempts=attempts-CASE WHEN sqlc.arg(control_deferred)::boolean THEN 1 ELSE 0 END,
    total_attempts=total_attempts-CASE WHEN sqlc.arg(control_deferred)::boolean THEN 1 ELSE 0 END,
    state = sqlc.arg(state)::text, available_at = sqlc.arg(available_at)::timestamptz,
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

-- name: EventRecipientAppendHistory :one
INSERT INTO event_fanout_attempt_history
    (outbox_id, app_id, subscription_id, action, state, attempts, failure_code, retryable, last_error, occurred_at, retry_stop_reason, filter_reason,
     capacity_scope,capacity_deferrals,details_truncated)
VALUES (sqlc.arg(outbox_id)::bigint, sqlc.arg(app_id)::uuid, sqlc.arg(subscription_id)::text,
        sqlc.arg(action)::text, sqlc.arg(state)::text, sqlc.arg(attempts)::integer,
        sqlc.arg(failure_code)::text, sqlc.arg(retryable)::boolean, sqlc.arg(last_error)::text,
        sqlc.arg(occurred_at)::timestamptz, sqlc.arg(retry_stop_reason)::text,sqlc.arg(filter_reason)::text,sqlc.arg(capacity_scope)::text,
        sqlc.arg(capacity_deferrals)::bigint,sqlc.arg(details_truncated)::boolean)
RETURNING id;

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
 delivery_deadline_at=CASE WHEN sqlc.arg(allow_expired)::boolean THEN NULL ELSE event_recipient_delivery_deadline(recipient,(SELECT o.created_at FROM event_fanout_outbox o WHERE o.id=outbox_id),'{}'::jsonb) END,
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
 AND (event_recipient_delivery_deadline(r.recipient,o.created_at,'{}'::jsonb) IS NULL OR event_recipient_delivery_deadline(r.recipient,o.created_at,'{}'::jsonb)>clock_timestamp())
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
 AND (event_recipient_delivery_deadline(r.recipient,o.created_at,'{}'::jsonb) IS NULL OR event_recipient_delivery_deadline(r.recipient,o.created_at,'{}'::jsonb)>clock_timestamp())
      AND ((sqlc.arg(event_source)::text = '' AND sqlc.arg(event_id)::text = '') OR
           (o.source = sqlc.arg(event_source)::text AND o.event_id = sqlc.arg(event_id)::text))
) AS has_more;

-- name: EventDeliveryLockCapacity :exec
INSERT INTO event_delivery_capacity (account_id,consumer_limit,app_limit,account_limit)
VALUES (sqlc.arg(account_id)::uuid,sqlc.arg(consumer_limit)::integer,sqlc.arg(app_limit)::integer,sqlc.arg(account_limit)::integer)
ON CONFLICT (account_id) DO UPDATE SET consumer_limit=excluded.consumer_limit,app_limit=excluded.app_limit,account_limit=excluded.account_limit;

-- name: EventDeliveryCounts :one
-- Reads the account's live slots (event_delivery_slots_live), not its whole
-- delivery history; the invocation's state still decides.
SELECT count(*)::bigint AS account_count,
 count(*) FILTER (WHERE s.app_id=sqlc.arg(app_id)::uuid)::bigint AS app_count,
 count(*) FILTER (WHERE s.app_id=sqlc.arg(app_id)::uuid AND s.subscription_id=sqlc.arg(subscription_id)::text)::bigint AS consumer_count
FROM event_delivery_slots s JOIN invocations i ON i.id=s.invocation_id
WHERE s.account_id=sqlc.arg(account_id)::uuid AND s.live AND i.state IN ('pending','dispatching')
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
   AND (sqlc.arg(for_recipient_adoption)::boolean OR NOT EXISTS (
     SELECT 1 FROM jsonb_array_elements(coalesce(o.recipient_snapshot,'[]'::jsonb)) item(recipient)
     WHERE coalesce(o.recipient_progress->(item.recipient->>'id')->>'state','pending')
         NOT IN ('enqueued','filtered','failed')
       AND event_recipient_order_blocked(o.id,item.recipient->>'id',item.recipient,false)
   ))
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

-- name: EventRoutingClaimReceiptsForAdoption :many
-- One scan claims up to batch_limit receipts for recipient adoption, as
-- repeated EventRoutingClaimReceipt adoption claims would: each round takes
-- one receipt from every eligible account in service order. Within an
-- account, receipts keep the consumer order they had before the batch.
WITH eligible AS MATERIALIZED (
 SELECT o.id, o.account_id,
   coalesce(fa.last_claimed_at,'epoch'::timestamptz) AS account_served,
   coalesce(cf.last_claimed_at,fa.last_claimed_at,'epoch'::timestamptz) AS consumer_served
 FROM event_fanout_outbox o
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
), rounds AS (
 SELECT e.*, row_number() OVER (PARTITION BY e.account_id ORDER BY e.consumer_served, e.id) AS account_round
 FROM eligible e
), account_firsts AS (
 -- Later rounds visit accounts in the order their first claims took.
 SELECT r.*, first_value(r.consumer_served) OVER account_order AS first_consumer_served,
   first_value(r.id) OVER account_order AS first_id
 FROM rounds r
 WINDOW account_order AS (PARTITION BY r.account_id ORDER BY r.account_round)
), ordered AS (
 SELECT f.id, row_number() OVER (ORDER BY f.account_round, f.account_served, f.first_consumer_served, f.first_id) AS position
 FROM account_firsts f
), candidate AS (
 -- Repeating the due test here re-checks a receipt another worker claimed
 -- after this statement's snapshot, before SKIP LOCKED could skip it.
 -- Only the first ranks can be claimed, so only they are looked up.
 SELECT o.id, k.position FROM event_fanout_outbox o JOIN ordered k ON k.id=o.id
 WHERE k.position <= 2 * sqlc.arg(batch_limit)::integer
   AND NOT o.recipient_claims AND ((o.state='pending' AND o.available_at<=sqlc.arg(now_at)::timestamptz)
   OR (o.state='processing' AND o.lease_until<=sqlc.arg(now_at)::timestamptz))
 ORDER BY k.position
 FOR UPDATE OF o SKIP LOCKED LIMIT sqlc.arg(batch_limit)::integer
), claimed AS (
 UPDATE event_fanout_outbox o SET state='processing',claim_token=gen_random_uuid(),
 lease_until=sqlc.arg(now_at)::timestamptz+interval '5 minutes',attempts=o.attempts+1
 FROM candidate c WHERE o.id=c.id RETURNING o.*, c.position
), stamp AS MATERIALIZED (
 SELECT clock_timestamp() AS at
), served AS (
 -- Each account and consumer is stamped at its last claim in the batch.
 SELECT c.account_id, ''::text AS subscription_id, max(c.position) AS position
 FROM claimed c GROUP BY c.account_id
 UNION ALL
 SELECT c.account_id, r->>'id', max(c.position)
 FROM claimed c CROSS JOIN LATERAL jsonb_array_elements(coalesce(c.recipient_snapshot,'[]'::jsonb)) r
 WHERE coalesce(c.recipient_progress->(r->>'id')->>'state','pending') NOT IN ('enqueued','filtered','failed')
   AND coalesce((c.recipient_progress->(r->>'id')->>'next_attempt_at')::timestamptz,'epoch'::timestamptz)<=sqlc.arg(now_at)::timestamptz
 GROUP BY c.account_id, r->>'id'
), fairness AS (
 -- Rows are written in key order, the lock order every claim uses.
 INSERT INTO event_routing_fairness
 SELECT s.account_id, s.subscription_id, t.at + s.position * interval '1 microsecond' FROM served s CROSS JOIN stamp t
 ORDER BY s.account_id, s.subscription_id
 ON CONFLICT (account_id,subscription_id) DO UPDATE SET last_claimed_at=excluded.last_claimed_at RETURNING account_id
)
SELECT c.id, c.claim_token, c.attempts, c.lease_until, c.created_at, c.payload, c.recipient_snapshot, c.recipient_progress
FROM claimed c ORDER BY c.position;

-- name: EventRoutingReleaseAdoptionClaim :execrows
-- Returns an unprocessed adoption claim as if it had not been made.
UPDATE event_fanout_outbox SET state='pending', claim_token=NULL, lease_until=NULL, attempts=attempts-1
WHERE id=sqlc.arg(id)::bigint AND claim_token=sqlc.arg(claim_token)::uuid
  AND state='processing' AND NOT recipient_claims;

-- name: EventDeliveryReplayAccount :one
SELECT s.account_id,a.plan FROM event_delivery_slots s JOIN accounts a ON a.id=s.account_id
WHERE s.invocation_id=sqlc.arg(invocation_id)::uuid;

-- name: EventRoutingDeferReceipt :execrows
UPDATE event_fanout_outbox o SET state='pending',claim_token=NULL,lease_until=NULL,
 available_at=coalesce((SELECT min(coalesce((p.value->>'next_attempt_at')::timestamptz,
 clock_timestamp()+interval '5 seconds')) FROM jsonb_each(o.recipient_progress) p WHERE p.value->>'state'='pending'),clock_timestamp()+interval '5 seconds')
WHERE o.id=sqlc.arg(id)::bigint AND o.claim_token=sqlc.arg(claim_token)::uuid
 AND o.state='processing' AND NOT o.recipient_claims AND o.lease_until>clock_timestamp();

-- name: EventHistoryObserve :one
INSERT INTO event_fanout_history_summaries AS s
 (outbox_id,subscription_id,app_id,observed_outcomes,capacity_deferrals,
  first_capacity_wait_at,last_capacity_wait_at,last_capacity_scope,last_outcome_capacity_scope)
VALUES (sqlc.arg(outbox_id)::bigint,sqlc.arg(subscription_id)::text,sqlc.arg(app_id)::uuid,1,
 sqlc.arg(capacity_deferrals)::bigint,
 CASE WHEN sqlc.arg(wait_scope)::text<>'' THEN sqlc.arg(occurred_at)::timestamptz END,
 CASE WHEN sqlc.arg(wait_scope)::text<>'' THEN sqlc.arg(occurred_at)::timestamptz END,
 sqlc.arg(wait_scope)::text,sqlc.arg(wait_scope)::text)
ON CONFLICT (outbox_id,subscription_id) DO UPDATE SET
 observed_outcomes=s.observed_outcomes+1,
 capacity_deferrals=greatest(s.capacity_deferrals,excluded.capacity_deferrals),
 first_capacity_wait_at=coalesce(s.first_capacity_wait_at,excluded.first_capacity_wait_at),
 last_capacity_wait_at=coalesce(excluded.last_capacity_wait_at,s.last_capacity_wait_at),
 last_capacity_scope=CASE WHEN excluded.last_capacity_scope<>'' THEN excluded.last_capacity_scope ELSE s.last_capacity_scope END,
 last_was_coalesced=excluded.last_outcome_capacity_scope<>'' AND s.last_outcome_capacity_scope=excluded.last_outcome_capacity_scope,
 coalesced_outcomes=s.coalesced_outcomes+CASE WHEN excluded.last_outcome_capacity_scope<>'' AND s.last_outcome_capacity_scope=excluded.last_outcome_capacity_scope THEN 1 ELSE 0 END,
 last_outcome_capacity_scope=excluded.last_outcome_capacity_scope
RETURNING last_was_coalesced;

-- name: EventHistoryMarkDetail :exec
UPDATE event_fanout_history_summaries SET latest_id=sqlc.arg(history_id)::bigint,
 latest_failure_id=CASE WHEN sqlc.arg(is_failure)::boolean THEN sqlc.arg(history_id)::bigint ELSE latest_failure_id END,
 latest_replay_id=CASE WHEN sqlc.arg(is_replay)::boolean THEN sqlc.arg(history_id)::bigint ELSE latest_replay_id END
WHERE outbox_id=sqlc.arg(outbox_id)::bigint AND subscription_id=sqlc.arg(subscription_id)::text;

-- name: EventHistoryCompact :execrows
WITH ranked AS (
 SELECT h.id,h.occurred_at,
   h.id IN (s.latest_id,s.latest_failure_id,s.latest_replay_id) AS protected,
   row_number() OVER (ORDER BY (h.id IN (s.latest_id,s.latest_failure_id,s.latest_replay_id)) DESC,h.id DESC) AS position,
   sum(h.history_bytes) OVER (ORDER BY (h.id IN (s.latest_id,s.latest_failure_id,s.latest_replay_id)) DESC,h.id DESC) AS bytes
 FROM event_fanout_attempt_history h JOIN event_fanout_history_summaries s
  ON s.outbox_id=h.outbox_id AND s.subscription_id=h.subscription_id
 WHERE h.outbox_id=sqlc.arg(outbox_id)::bigint AND h.subscription_id=sqlc.arg(subscription_id)::text
), removed AS (
 DELETE FROM event_fanout_attempt_history h USING ranked r WHERE h.id=r.id AND
 (r.position>sqlc.arg(max_rows)::bigint OR r.bytes>sqlc.arg(max_bytes)::bigint OR
  (NOT r.protected AND r.occurred_at<=sqlc.arg(before_at)::timestamptz))
 RETURNING h.id,h.occurred_at
)
UPDATE event_fanout_history_summaries SET
 compacted_outcomes=compacted_outcomes+(SELECT count(*) FROM removed),
 compacted_through_id=greatest(compacted_through_id,coalesce((SELECT max(id) FROM removed),0)),
 compacted_through_at=greatest(compacted_through_at,(SELECT max(occurred_at) FROM removed))
WHERE outbox_id=sqlc.arg(outbox_id)::bigint AND subscription_id=sqlc.arg(subscription_id)::text;

-- name: EventHistorySchedulePrune :exec
UPDATE event_fanout_history_summaries s SET next_prune_at=(
 SELECT min(h.occurred_at)+sqlc.arg(retention_seconds)::bigint * interval '1 second'
 FROM event_fanout_attempt_history h WHERE h.outbox_id=s.outbox_id AND h.subscription_id=s.subscription_id
 AND h.id NOT IN (s.latest_id,s.latest_failure_id,s.latest_replay_id))
WHERE s.outbox_id=sqlc.arg(outbox_id)::bigint AND s.subscription_id=sqlc.arg(subscription_id)::text;

-- name: EventHistoryPruneCandidates :many
-- Take parent locks before summary/detail locks, matching routing and replay.
SELECT o.id FROM event_fanout_history_summaries s
JOIN event_fanout_outbox o ON o.id=s.outbox_id
WHERE s.next_prune_at<=sqlc.arg(now_at)::timestamptz
ORDER BY s.next_prune_at,s.outbox_id,s.subscription_id
LIMIT sqlc.arg(batch_limit)::integer FOR UPDATE OF o SKIP LOCKED;

-- name: EventHistoryDueRecipients :many
SELECT subscription_id FROM event_fanout_history_summaries
WHERE outbox_id=sqlc.arg(outbox_id)::bigint AND next_prune_at<=sqlc.arg(now_at)::timestamptz
ORDER BY subscription_id LIMIT sqlc.arg(batch_limit)::integer;

-- name: EventHistoryList :many
SELECT h.*,o.event_id,o.source AS event_source,o.event_type
FROM event_fanout_attempt_history h JOIN event_fanout_outbox o ON o.id=h.outbox_id
JOIN apps a ON a.id=h.app_id AND a.account_id=o.account_id
WHERE h.app_id=sqlc.arg(app_id)::uuid AND (sqlc.arg(event_source)::text='' OR o.source=sqlc.arg(event_source)::text) AND (sqlc.arg(event_id)::text='' OR o.event_id=sqlc.arg(event_id)::text)
 AND (sqlc.arg(subscription_id)::text='' OR h.subscription_id=sqlc.arg(subscription_id)::text)
 AND (sqlc.arg(before_id)::bigint=0 OR h.id<sqlc.arg(before_id)::bigint)
ORDER BY h.id DESC LIMIT sqlc.arg(page_limit)::integer;

-- name: EventHistorySummaries :many
SELECT s.*, (SELECT count(*) FROM event_fanout_attempt_history h WHERE h.outbox_id=s.outbox_id AND h.subscription_id=s.subscription_id)::bigint AS retained_records,
 (SELECT coalesce(sum(h.history_bytes),0)::bigint FROM event_fanout_attempt_history h WHERE h.outbox_id=s.outbox_id AND h.subscription_id=s.subscription_id)::bigint AS retained_bytes
FROM event_fanout_history_summaries s JOIN event_fanout_outbox o ON o.id=s.outbox_id
JOIN apps a ON a.id=s.app_id AND a.account_id=o.account_id
WHERE s.app_id=sqlc.arg(app_id)::uuid AND (sqlc.arg(event_source)::text='' OR o.source=sqlc.arg(event_source)::text) AND (sqlc.arg(event_id)::text='' OR o.event_id=sqlc.arg(event_id)::text)
 AND (sqlc.arg(subscription_id)::text='' OR s.subscription_id=sqlc.arg(subscription_id)::text)
ORDER BY s.subscription_id;

-- name: EventRecipientGuardedReplayReceipt :one
SELECT o.id, o.created_at, o.state, o.recipient_claims,
       (o.recipient_progress -> sqlc.arg(subscription_id)::text)::jsonb AS progress,
       EXISTS (SELECT 1 FROM jsonb_array_elements(COALESCE(o.recipient_snapshot, '[]'::jsonb)) r(recipient)
         WHERE r.recipient->>'app_id' = sqlc.arg(app_id)::text
           AND r.recipient->>'id' = sqlc.arg(subscription_id)::text) AS recipient_found
FROM event_fanout_outbox o
WHERE o.account_id = sqlc.arg(account_id)::uuid AND o.source = sqlc.arg(event_source)::text
  AND o.event_id = sqlc.arg(event_id)::text
FOR UPDATE OF o;

-- name: EventRecipientReplayGenerationGuard :one
SELECT generation, state FROM event_fanout_recipients
WHERE outbox_id = sqlc.arg(outbox_id)::bigint AND app_id = sqlc.arg(app_id)::uuid
  AND subscription_id = sqlc.arg(subscription_id)::text
FOR UPDATE;
-- name: EventRecoveryPublishedVersion :one
SELECT published_version FROM workflow_automation_definitions
WHERE app_id=sqlc.arg(app_id)::uuid AND name=sqlc.arg(name)::text;

-- name: EventRecoveryReplayCount :one
WITH locked AS MATERIALIZED (
 SELECT id FROM event_fanout_attempt_history
 WHERE outbox_id=sqlc.arg(outbox_id)::bigint AND subscription_id=sqlc.arg(subscription_id)::text AND action='operator_replay'
 FOR SHARE
) SELECT count(*)::bigint FROM locked;

-- name: EventRecoveryRoutingGeneration :one
SELECT generation, state FROM event_fanout_recipients
WHERE outbox_id=sqlc.arg(outbox_id)::bigint AND subscription_id=sqlc.arg(subscription_id)::text
FOR UPDATE;

-- name: EventRecoveryLegacySettled :one
SELECT state='delivered' AS settled FROM event_fanout_outbox WHERE id=$1;

-- name: EventWorkflowRecoveryCandidate :one
SELECT o.id AS outbox_id, o.source AS event_source,o.event_id,o.event_type,
 (r.recipient->>'id')::text AS subscription_id,
 coalesce((p.progress->>'updated_at')::timestamptz,o.created_at)::timestamptz AS failed_at,
 coalesce(nullif(p.progress->>'failure_code',''),'unknown')::text AS failure_code,
 coalesce((p.progress->>'retryable')::boolean,false)::boolean AS retryable,
 event_recovery_failure_identity(p.progress)::jsonb AS expected_progress
FROM event_fanout_outbox o
JOIN apps a ON a.id=sqlc.arg(app_id)::uuid AND a.account_id=o.account_id AND a.status<>'deleted'
CROSS JOIN LATERAL jsonb_array_elements(coalesce(o.recipient_snapshot,'[]'::jsonb)) r(recipient)
CROSS JOIN LATERAL (SELECT o.recipient_progress->(r.recipient->>'id') AS progress) p
WHERE o.account_id=sqlc.arg(account_id)::uuid AND o.source=sqlc.arg(event_source)::text AND o.event_id=sqlc.arg(event_id)::text
 AND r.recipient->>'app_id'=a.id::text AND r.recipient->>'id'=sqlc.arg(subscription_id)::text
 AND r.recipient->'workflow'->>'name'=sqlc.arg(workflow_name)::text
 AND p.progress->>'state'='failed';
