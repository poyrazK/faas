-- name: EventReplayBackfillCreate :one
INSERT INTO event_replay_jobs
    (account_id, app_id, subscription_id, subscription_revision, recipient, consumer_kind, workflow_name,
     from_at, until_at, cutoff_at, earliest_retained_at, cursor_at)
VALUES (sqlc.arg(account_id)::uuid, sqlc.arg(app_id)::uuid, sqlc.arg(subscription_id)::uuid,
        sqlc.arg(subscription_revision)::text, sqlc.arg(recipient)::jsonb,
        sqlc.arg(consumer_kind)::text, sqlc.arg(workflow_name)::text,
        sqlc.arg(from_at)::timestamptz, sqlc.arg(until_at)::timestamptz,
        sqlc.arg(cutoff_at)::timestamptz, sqlc.arg(earliest_retained_at)::timestamptz,
        sqlc.arg(cursor_at)::timestamptz)
RETURNING id;

-- name: EventReplayBackfillWorkflowTarget :many
SELECT a.id AS app_id, a.account_id, a.slug AS app_slug, d.id AS deployment_id,
       definition.value AS workflow, ac.plan
FROM apps a
JOIN accounts ac ON ac.id=a.account_id
JOIN LATERAL (
    SELECT dep.id, dep.workflows FROM deployments dep
    WHERE dep.app_id=a.id AND dep.status='live' AND dep.scope='default'
    ORDER BY (dep.traffic_percent > 0) DESC, dep.created_at DESC, dep.id DESC
    LIMIT 1
) d ON true
CROSS JOIN LATERAL jsonb_array_elements(app_workflow_definitions(a.id,d.workflows)) definition(value)
WHERE a.id=sqlc.arg(app_id)::uuid AND a.account_id=sqlc.arg(account_id)::uuid
  AND a.status <> 'deleted' AND NOT a.maintenance_mode AND NOT a.platform_tenant_required
  AND ac.status IN ('active','past_due') AND ac.abuse_hold_at IS NULL AND ac.plan <> 'free'
  AND definition.value->>'name'=sqlc.arg(workflow_name)::text
  AND definition.value->'trigger'->>'type'='event'
  AND coalesce(definition.value->'trigger'->>'enabled','true')='true'
LIMIT 2;

-- name: EventReplayBackfillGet :one
SELECT j.id, j.account_id, j.app_id, a.slug AS app_slug, j.subscription_id,
       j.subscription_revision, j.consumer_kind, j.workflow_name, j.from_at, j.until_at, j.cutoff_at,
       j.earliest_retained_at, j.duplicate_policy, j.state, j.scan_complete,
       j.scanned_count, j.matched_count, j.filtered_count,
       j.created_at, j.updated_at, j.completed_at,
       count(i.outbox_id) FILTER (WHERE i.state='pending')::bigint AS pending_count,
       count(i.outbox_id) FILTER (WHERE i.state='processing')::bigint AS processing_count,
       count(i.outbox_id) FILTER (WHERE i.state='enqueued')::bigint AS enqueued_count,
       count(i.outbox_id) FILTER (WHERE i.state='failed')::bigint AS failed_count,
       count(i.outbox_id) FILTER (WHERE i.state='failed' AND i.retryable)::bigint AS retryable_failed_count,
       count(i.outbox_id) FILTER (WHERE i.state='skipped_captured')::bigint AS skipped_captured_count,
       count(i.outbox_id) FILTER (WHERE i.state='skipped_unknown')::bigint AS skipped_unknown_count,
       count(i.outbox_id) FILTER (WHERE i.state='skipped_existing')::bigint AS skipped_existing_count,
       count(i.outbox_id) FILTER (WHERE i.state='skipped_unsettled')::bigint AS skipped_unsettled_count
FROM event_replay_jobs j
JOIN apps a ON a.id=j.app_id AND a.account_id=j.account_id
LEFT JOIN event_replay_job_items i ON i.job_id=j.id
WHERE j.id=sqlc.arg(id)::uuid AND j.account_id=sqlc.arg(account_id)::uuid
GROUP BY j.id, a.slug;

-- name: EventReplayBackfillExists :one
SELECT EXISTS (SELECT 1 FROM event_replay_jobs
               WHERE id=sqlc.arg(id)::uuid AND account_id=sqlc.arg(account_id)::uuid);

-- name: EventReplayBackfillItems :many
SELECT i.outbox_id, i.accepted_at, i.event_source, i.event_id, i.event_type,
       i.schema_version, i.state, i.attempts, i.failure_code, i.last_error,
       i.retryable, i.updated_at, j.subscription_id, j.consumer_kind, j.workflow_name,
       coalesce(wr.id::text,'')::text AS workflow_run_id, coalesce(wr.status,'')::text AS workflow_run_status,
       (o.id IS NOT NULL)::boolean AS receipt_available,
       (o.id IS NOT NULL AND a.id IS NOT NULL AND (r.subscription_id IS NOT NULL OR EXISTS (
           SELECT 1 FROM jsonb_array_elements(coalesce(o.recipient_snapshot,'[]'::jsonb)) s(recipient)
           WHERE s.recipient->>'id'=j.subscription_id::text AND s.recipient->>'app_id'=j.app_id::text
       )))::boolean AS execution_history_available
FROM event_replay_job_items i
JOIN event_replay_jobs j ON j.id=i.job_id
LEFT JOIN event_fanout_outbox o ON o.id=i.outbox_id AND o.account_id=j.account_id
    AND o.source=i.event_source AND o.event_id=i.event_id AND o.created_at=i.accepted_at
LEFT JOIN apps a ON a.id=j.app_id AND a.account_id=j.account_id
LEFT JOIN event_fanout_recipients r ON r.outbox_id=o.id AND r.subscription_id=j.subscription_id::text AND r.app_id=j.app_id
LEFT JOIN workflow_event_receipts wer ON wer.outbox_id=o.id AND wer.recipient_id=j.subscription_id
LEFT JOIN workflow_runs wr ON wr.id=wer.run_id
WHERE i.job_id=sqlc.arg(job_id)::uuid
  AND (sqlc.arg(state)::text = '' OR i.state=sqlc.arg(state)::text)
  AND (i.accepted_at,i.outbox_id) > (sqlc.arg(after_at)::timestamptz,sqlc.arg(after_outbox_id)::bigint)
ORDER BY i.accepted_at,i.outbox_id
LIMIT sqlc.arg(page_limit)::integer;

-- name: EventReplayBackfillActiveJobCount :one
SELECT count(*)::bigint FROM event_replay_jobs
WHERE account_id=sqlc.arg(account_id)::uuid AND state='running';

-- name: EventReplayBackfillLockAccountRange :exec
SELECT pg_advisory_xact_lock(hashtextextended(sqlc.arg(account_id)::uuid::text,625));

-- name: EventReplayBackfillNextJob :one
SELECT * FROM event_replay_jobs WHERE state='running' AND NOT scan_complete
  AND (SELECT count(*) FROM event_replay_job_items i
       WHERE i.job_id=event_replay_jobs.id AND i.state IN ('pending','processing')) < sqlc.arg(in_flight_max)::bigint
ORDER BY created_at,id FOR UPDATE SKIP LOCKED LIMIT 1;

-- name: EventReplayBackfillLockJob :one
SELECT id FROM event_replay_jobs
WHERE id=sqlc.arg(id)::uuid AND state='running' FOR UPDATE;

-- name: EventReplayBackfillLockAnyJob :one
SELECT id FROM event_replay_jobs WHERE id=sqlc.arg(id)::uuid
  AND account_id=sqlc.arg(account_id)::uuid FOR UPDATE;

-- name: EventReplayBackfillInFlightCount :one
SELECT count(*)::bigint FROM event_replay_job_items
WHERE job_id=sqlc.arg(job_id)::uuid AND state IN ('pending','processing');

-- name: EventReplayBackfillCandidates :many
SELECT o.id, o.created_at, o.payload, o.recipient_snapshot, o.state,
       o.source, o.event_id, o.event_type, coalesce(o.schema_version,'') AS schema_version,
       CASE WHEN o.recipient_snapshot IS NULL THEN 'unknown'
            WHEN EXISTS (SELECT 1 FROM jsonb_array_elements(o.recipient_snapshot) s(recipient)
                         WHERE s.recipient->>'id'=sqlc.arg(subscription_id)::text
                           AND s.recipient->>'app_id'=sqlc.arg(app_id)::text)
            THEN 'captured' ELSE 'not_captured' END::text AS original_recipient,
       EXISTS (SELECT 1 FROM event_fanout_recipients r
               WHERE r.outbox_id=o.id AND r.subscription_id=sqlc.arg(subscription_id)::text) AS recipient_exists,
       EXISTS (SELECT 1 FROM workflow_event_receipts wr
               WHERE wr.outbox_id=o.id AND wr.recipient_id=sqlc.arg(subscription_id)::uuid) AS workflow_admission_exists
FROM event_fanout_outbox o
WHERE o.account_id=sqlc.arg(account_id)::uuid
  AND o.created_at >= sqlc.arg(from_at)::timestamptz
  AND o.created_at < sqlc.arg(cutoff_at)::timestamptz
  AND (o.created_at,o.id) > (sqlc.arg(cursor_at)::timestamptz,sqlc.arg(cursor_outbox_id)::bigint)
ORDER BY o.created_at,o.id
LIMIT sqlc.arg(page_limit)::integer;

-- name: EventReplayBackfillInsertWorkflowFailure :execrows
INSERT INTO event_replay_job_items(job_id,outbox_id,accepted_at,event_source,event_id,event_type,schema_version,
                                   state,attempts,failure_code,last_error,retryable)
VALUES (sqlc.arg(job_id)::uuid,sqlc.arg(outbox_id)::bigint,sqlc.arg(accepted_at)::timestamptz,
        sqlc.arg(event_source)::text,sqlc.arg(event_id)::text,sqlc.arg(event_type)::text,sqlc.arg(schema_version)::text,
        'failed',1,sqlc.arg(failure_code)::text,sqlc.arg(last_error)::text,sqlc.arg(retryable)::boolean)
ON CONFLICT (job_id,outbox_id) DO NOTHING;

-- name: EventReplayBackfillWorkflowRetryCandidates :many
SELECT i.outbox_id, i.accepted_at, i.event_source, i.event_id, i.event_type, i.schema_version,
       i.attempts, o.payload, o.recipient_snapshot, o.state,
       CASE WHEN o.recipient_snapshot IS NULL THEN 'unknown'
            WHEN EXISTS (SELECT 1 FROM jsonb_array_elements(o.recipient_snapshot) s(recipient)
                         WHERE s.recipient->>'id'=j.subscription_id::text
                           AND s.recipient->>'app_id'=j.app_id::text)
            THEN 'captured' ELSE 'not_captured' END::text AS original_recipient,
       EXISTS (SELECT 1 FROM event_fanout_recipients r
               WHERE r.outbox_id=o.id AND r.subscription_id=j.subscription_id::text) AS recipient_exists,
       EXISTS (SELECT 1 FROM workflow_event_receipts wr
               WHERE wr.outbox_id=o.id AND wr.recipient_id=j.subscription_id) AS workflow_admission_exists
FROM event_replay_job_items i
JOIN event_replay_jobs j ON j.id=i.job_id
JOIN event_fanout_outbox o ON o.id=i.outbox_id AND o.account_id=j.account_id
    AND o.source=i.event_source AND o.event_id=i.event_id AND o.created_at=i.accepted_at
WHERE i.job_id=sqlc.arg(job_id)::uuid AND j.account_id=sqlc.arg(account_id)::uuid
  AND j.consumer_kind='workflow' AND i.state='failed' AND i.retryable
ORDER BY i.accepted_at,i.outbox_id LIMIT sqlc.arg(page_limit)::integer;

-- name: EventReplayBackfillWorkflowExpireRetry :execrows
UPDATE event_replay_job_items i SET failure_code='target_unavailable',
       last_error='source event is no longer retained',retryable=false,updated_at=clock_timestamp()
FROM event_replay_jobs j
WHERE i.job_id=j.id AND i.job_id=sqlc.arg(job_id)::uuid
  AND j.account_id=sqlc.arg(account_id)::uuid AND j.consumer_kind='workflow'
  AND i.state='failed' AND i.retryable
  AND NOT EXISTS (SELECT 1 FROM event_fanout_outbox o
                  WHERE o.id=i.outbox_id AND o.account_id=j.account_id
                    AND o.source=i.event_source AND o.event_id=i.event_id AND o.created_at=i.accepted_at);

-- name: EventReplayBackfillTargetSnapshot :one
SELECT recipient, app_id, account_id, consumer_kind, workflow_name FROM event_replay_jobs
WHERE id=sqlc.arg(job_id)::uuid AND account_id=sqlc.arg(account_id)::uuid;

-- name: EventReplayBackfillWorkflowFinishRetry :execrows
UPDATE event_replay_job_items SET state=sqlc.arg(state)::text, attempts=attempts+1,
       failure_code=sqlc.arg(failure_code)::text,last_error=sqlc.arg(last_error)::text,
       retryable=sqlc.arg(retryable)::boolean,updated_at=clock_timestamp()
WHERE job_id=sqlc.arg(job_id)::uuid AND outbox_id=sqlc.arg(outbox_id)::bigint
  AND state='failed' AND retryable;

-- name: EventReplayBackfillWorkflowRetryableCount :one
SELECT count(*)::bigint FROM event_replay_job_items i
JOIN event_replay_jobs j ON j.id=i.job_id
WHERE i.job_id=sqlc.arg(job_id)::uuid AND j.account_id=sqlc.arg(account_id)::uuid
  AND j.consumer_kind='workflow' AND i.state='failed' AND i.retryable;

-- name: EventReplayBackfillLockParent :one
SELECT * FROM event_fanout_outbox WHERE id=sqlc.arg(id)::bigint FOR UPDATE;

-- name: EventReplayBackfillAdoptReceipt :execrows
UPDATE event_fanout_outbox
SET recipient_claims=true
WHERE id=sqlc.arg(id)::bigint AND state='delivered'
  AND NOT recipient_claims AND recipient_snapshot IS NOT NULL;

-- name: EventReplayBackfillMaterializeSnapshot :exec
INSERT INTO event_fanout_recipients
    (outbox_id,subscription_id,app_id,recipient,state,total_attempts,capacity_deferrals,available_at)
SELECT o.id,s.recipient->>'id',(s.recipient->>'app_id')::uuid,s.recipient,
       CASE WHEN p.outcome->>'state' IN ('filtered','enqueued','failed') THEN p.outcome->>'state' ELSE 'enqueued' END,
       coalesce(nullif(p.outcome->>'attempts','')::integer,0),
       coalesce(nullif(p.outcome->>'capacity_deferrals','')::integer,0),o.created_at
FROM event_fanout_outbox o
CROSS JOIN LATERAL jsonb_array_elements(o.recipient_snapshot) s(recipient)
LEFT JOIN LATERAL (SELECT o.recipient_progress->(s.recipient->>'id') AS outcome) p ON true
WHERE o.id=sqlc.arg(id)::bigint
ON CONFLICT (outbox_id,subscription_id) DO NOTHING;

-- name: EventReplayBackfillInsertItem :execrows
INSERT INTO event_replay_job_items(job_id,outbox_id,accepted_at,event_source,event_id,event_type,schema_version,state,attempts,failure_code,last_error)
VALUES (sqlc.arg(job_id)::uuid,sqlc.arg(outbox_id)::bigint,sqlc.arg(accepted_at)::timestamptz,
        sqlc.arg(event_source)::text,sqlc.arg(event_id)::text,sqlc.arg(event_type)::text,sqlc.arg(schema_version)::text,
        sqlc.arg(state)::text,sqlc.arg(attempts)::integer,sqlc.arg(failure_code)::text,sqlc.arg(last_error)::text)
ON CONFLICT (job_id,outbox_id) DO NOTHING;

-- name: EventReplayBackfillInsertRecipient :execrows
INSERT INTO event_fanout_recipients
    (outbox_id,subscription_id,app_id,recipient,state,total_attempts,available_at,backfill_job_id,receipt_position,delivery_deadline_at)
VALUES (sqlc.arg(outbox_id)::bigint,sqlc.arg(subscription_id)::text,sqlc.arg(app_id)::uuid,
        sqlc.arg(recipient)::jsonb,'pending',0,sqlc.arg(available_at)::timestamptz,sqlc.arg(job_id)::uuid,
        (SELECT greatest(jsonb_array_length(coalesce(o.recipient_snapshot,'[]'::jsonb)),
                         coalesce((SELECT max(r.receipt_position) FROM event_fanout_recipients r WHERE r.outbox_id=o.id),0))+1
         FROM event_fanout_outbox o WHERE o.id=sqlc.arg(outbox_id)::bigint),
 (SELECT event_recipient_delivery_deadline(sqlc.arg(recipient)::jsonb,o.created_at,'{}'::jsonb) FROM event_fanout_outbox o WHERE o.id=sqlc.arg(outbox_id)::bigint))
ON CONFLICT (outbox_id,subscription_id) DO NOTHING;

-- name: EventReplayBackfillAdvance :exec
UPDATE event_replay_jobs SET cursor_at=sqlc.arg(cursor_at)::timestamptz,
       cursor_outbox_id=sqlc.arg(cursor_outbox_id)::bigint,
       scanned_count=scanned_count+sqlc.arg(scanned_delta)::bigint,
       matched_count=matched_count+sqlc.arg(matched_delta)::bigint,
       filtered_count=filtered_count+sqlc.arg(filtered_delta)::bigint,
       skipped_captured_count=skipped_captured_count+sqlc.arg(skipped_captured_delta)::bigint,
       skipped_unknown_count=skipped_unknown_count+sqlc.arg(skipped_unknown_delta)::bigint,
       skipped_existing_count=skipped_existing_count+sqlc.arg(skipped_existing_delta)::bigint,
       skipped_unsettled_count=skipped_unsettled_count+sqlc.arg(skipped_unsettled_delta)::bigint,
       updated_at=clock_timestamp()
WHERE id=sqlc.arg(job_id)::uuid AND state='running';

-- name: EventReplayBackfillMarkScanned :exec
UPDATE event_replay_jobs SET scan_complete=true,updated_at=clock_timestamp()
WHERE id=sqlc.arg(id)::uuid AND state='running';

-- name: EventReplayBackfillFinishItem :execrows
UPDATE event_replay_job_items SET state=sqlc.arg(state)::text,
       attempts=sqlc.arg(attempts)::integer,failure_code=sqlc.arg(failure_code)::text,
       last_error=sqlc.arg(last_error)::text,retryable=sqlc.arg(retryable)::boolean,
       updated_at=clock_timestamp()
WHERE job_id=sqlc.arg(job_id)::uuid AND outbox_id=sqlc.arg(outbox_id)::bigint
  AND state IN ('pending','processing');

-- name: EventReplayBackfillFinalize :exec
UPDATE event_replay_jobs j SET
    state=CASE WHEN EXISTS (SELECT 1 FROM event_replay_job_items i WHERE i.job_id=j.id AND i.state IN ('pending','processing')) THEN 'running'
               WHEN EXISTS (SELECT 1 FROM event_replay_job_items i WHERE i.job_id=j.id AND i.state='failed') THEN 'completed_with_failures'
               ELSE 'completed' END,
    completed_at=CASE WHEN EXISTS (SELECT 1 FROM event_replay_job_items i WHERE i.job_id=j.id AND i.state IN ('pending','processing')) THEN NULL ELSE clock_timestamp() END,
    updated_at=clock_timestamp()
WHERE j.id=sqlc.arg(id)::uuid AND j.scan_complete;

-- name: EventReplayBackfillClaimTarget :one
SELECT j.recipient FROM event_replay_jobs j
JOIN event_replay_job_items i ON i.job_id=j.id
JOIN event_fanout_recipients r ON r.backfill_job_id=j.id AND r.outbox_id=i.outbox_id
JOIN event_fanout_outbox o ON o.id=r.outbox_id AND o.account_id=j.account_id
JOIN apps a ON a.id=j.app_id AND a.account_id=j.account_id
WHERE j.id=sqlc.arg(job_id)::uuid AND j.state='running' AND i.outbox_id=sqlc.arg(outbox_id)::bigint
  AND i.state='processing' AND j.subscription_id=sqlc.arg(subscription_id)::uuid
  AND r.subscription_id=j.subscription_id::text AND r.app_id=j.app_id
  AND r.recipient=j.recipient;

-- name: EventReplayBackfillClaimValid :one
SELECT EXISTS (
    SELECT 1 FROM event_replay_jobs j
    JOIN event_replay_job_items i ON i.job_id=j.id
    JOIN event_fanout_recipients r ON r.backfill_job_id=j.id AND r.outbox_id=i.outbox_id
    JOIN event_fanout_outbox o ON o.id=r.outbox_id AND o.account_id=j.account_id
    JOIN apps a ON a.id=j.app_id AND a.account_id=j.account_id
    WHERE j.id=sqlc.arg(job_id)::uuid AND j.state='running'
      AND i.outbox_id=sqlc.arg(outbox_id)::bigint AND i.state='processing'
      AND j.subscription_id=sqlc.arg(subscription_id)::uuid
      AND r.subscription_id=j.subscription_id::text AND r.app_id=j.app_id AND r.recipient=j.recipient
      AND r.state='processing' AND r.generation=sqlc.arg(generation)::bigint
      AND r.claim_token=sqlc.arg(claim_token)::uuid AND r.lease_until>clock_timestamp()
);

-- name: EventReplayBackfillRetryCandidates :many
SELECT i.outbox_id,j.recipient,r.total_attempts,r.capacity_deferrals
FROM event_replay_job_items i
JOIN event_replay_jobs j ON j.id=i.job_id
JOIN event_fanout_recipients r ON r.outbox_id=i.outbox_id AND r.subscription_id=j.subscription_id::text AND r.backfill_job_id=j.id
JOIN event_fanout_outbox o ON o.id=i.outbox_id AND o.account_id=j.account_id
JOIN apps a ON a.id=j.app_id AND a.account_id=j.account_id
WHERE i.job_id=sqlc.arg(job_id)::uuid AND j.account_id=sqlc.arg(account_id)::uuid
  AND i.state='failed' AND (i.retryable OR sqlc.arg(allow_expired)::boolean AND i.failure_code='delivery_expired') AND r.state='failed'
 AND (sqlc.arg(allow_expired)::boolean OR event_recipient_delivery_deadline(r.recipient,o.created_at,'{}'::jsonb) IS NULL OR event_recipient_delivery_deadline(r.recipient,o.created_at,'{}'::jsonb)>clock_timestamp())
  AND (sqlc.arg(event_source)::text='' OR o.source=sqlc.arg(event_source)::text)
  AND (sqlc.arg(event_id)::text='' OR o.event_id=sqlc.arg(event_id)::text)
  AND (sqlc.arg(app_id)::text='' OR j.app_id::text=sqlc.arg(app_id)::text)
ORDER BY i.accepted_at,i.outbox_id LIMIT sqlc.arg(page_limit)::integer;

-- name: EventReplayBackfillRecipientJob :one
-- Read provenance before taking the job lock, preserving job -> parent order.
SELECT j.id FROM event_fanout_outbox o
JOIN event_fanout_recipients r ON r.outbox_id=o.id
JOIN event_replay_jobs j ON j.id=r.backfill_job_id AND j.account_id=o.account_id
    AND j.app_id=r.app_id AND j.subscription_id::text=r.subscription_id
JOIN apps a ON a.id=j.app_id AND a.account_id=j.account_id
WHERE o.account_id=sqlc.arg(account_id)::uuid AND o.source=sqlc.arg(event_source)::text
  AND o.event_id=sqlc.arg(event_id)::text AND r.app_id=sqlc.arg(app_id)::uuid
  AND r.subscription_id=sqlc.arg(subscription_id)::text AND r.receipt_position IS NOT NULL;

-- name: EventReplayBackfillResetItem :execrows
UPDATE event_replay_job_items SET state='pending',failure_code='',last_error='',retryable=false,updated_at=clock_timestamp()
WHERE job_id=sqlc.arg(job_id)::uuid AND outbox_id=sqlc.arg(outbox_id)::bigint AND state='failed';

-- name: EventReplayBackfillCountRetryableFailed :one
SELECT count(*)::bigint FROM event_replay_job_items i
JOIN event_fanout_recipients r ON r.outbox_id=i.outbox_id AND r.backfill_job_id=i.job_id
JOIN event_fanout_outbox o ON o.id=r.outbox_id
 WHERE i.job_id=sqlc.arg(job_id)::uuid AND i.state='failed' AND i.retryable AND r.state='failed'
 AND (event_recipient_delivery_deadline(r.recipient,o.created_at,'{}'::jsonb) IS NULL OR event_recipient_delivery_deadline(r.recipient,o.created_at,'{}'::jsonb)>clock_timestamp());

-- name: EventReplayBackfillSetRunning :exec
UPDATE event_replay_jobs SET state='running',completed_at=NULL,updated_at=clock_timestamp()
WHERE id=sqlc.arg(id)::uuid AND state='completed_with_failures';

-- name: EventReplayBackfillPruneJobs :execrows
DELETE FROM event_replay_jobs WHERE id IN (
    SELECT id FROM event_replay_jobs WHERE state <> 'running' AND completed_at < sqlc.arg(cutoff_at)::timestamptz
    ORDER BY completed_at,id LIMIT sqlc.arg(page_limit)::integer FOR UPDATE SKIP LOCKED
);

-- name: EventReplayBackfillPruneEnvelopes :execrows
WITH picked AS MATERIALIZED (
    SELECT o.id,o.account_id FROM event_fanout_outbox o
WHERE o.state='delivered' AND o.delivered_at < sqlc.arg(before_at)::timestamptz
      AND event_receipt_retention_hold(o.account_id,o.id,o.created_at,sqlc.arg(job_cutoff_at)::timestamptz)=''
    ORDER BY o.delivered_at,o.id LIMIT sqlc.arg(page_limit)::integer FOR UPDATE OF o SKIP LOCKED
), unlocked AS MATERIALIZED (
    SELECT id FROM picked WHERE pg_try_advisory_xact_lock(hashtextextended(account_id::text,625))
)
DELETE FROM event_fanout_outbox WHERE id IN (SELECT id FROM unlocked);
