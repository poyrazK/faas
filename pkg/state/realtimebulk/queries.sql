-- name: PreviewDeadLetters :many
SELECT m.sequence::text AS sequence,m.dead_since,m.attempts,m.failure_code,
 (sqlc.arg(ordered)::boolean AND EXISTS(SELECT 1 FROM managed_realtime_consumer_group_messages b WHERE b.group_id=m.group_id AND b.ordering_key=m.ordering_key AND b.status='leased' AND b.lease_until>clock_timestamp()))::boolean AS ordering_blocked,
 (CASE WHEN sqlc.arg(retention_seconds)::integer>0 THEN m.dead_since+make_interval(secs=>sqlc.arg(retention_seconds)::double precision) END)::timestamptz AS expires_at
 FROM managed_realtime_consumer_group_messages m WHERE m.group_id=sqlc.arg(group_id)::text::uuid AND m.status='dead'
 AND m.sequence>sqlc.arg(after_sequence)::bigint AND (sqlc.arg(through_sequence)::bigint=0 OR m.sequence<=sqlc.arg(through_sequence)::bigint)
 AND (cardinality(sqlc.arg(failure_codes)::text[])=0 OR m.failure_code=ANY(sqlc.arg(failure_codes)::text[]))
 ORDER BY m.sequence LIMIT sqlc.arg(row_limit)::integer;

-- name: ReadReplaySelection :one
SELECT status,ordering_key,dead_since,deadline_invalid,coalesce(delivery_deadline<=clock_timestamp(),false)::boolean AS deadline_expired FROM managed_realtime_consumer_group_messages WHERE group_id=sqlc.arg(group_id)::text::uuid AND sequence=sqlc.arg(sequence)::bigint;

-- name: HasOrderingLease :one
SELECT EXISTS(SELECT 1 FROM managed_realtime_consumer_group_messages WHERE group_id=sqlc.arg(group_id)::text::uuid AND ordering_key=sqlc.arg(ordering_key)::text AND status='leased')::boolean;

-- name: ReplayDeadLetter :exec
UPDATE managed_realtime_consumer_group_messages SET status='ready',attempts=0,worker='',token_hash=NULL,lease_until=NULL,
 failure_code='',credential_id=NULL,available_at=clock_timestamp() WHERE group_id=sqlc.arg(group_id)::text::uuid AND sequence=sqlc.arg(sequence)::bigint AND status='dead';

-- name: FreezeDeadline :exec
UPDATE managed_realtime_consumer_group_messages SET delivery_deadline=sqlc.narg(delivery_deadline)::timestamptz,deadline_invalid=sqlc.arg(deadline_invalid)::boolean WHERE group_id=sqlc.arg(group_id)::text::uuid AND sequence=sqlc.arg(sequence)::bigint;

-- name: ExpireDeadlines :exec
UPDATE managed_realtime_consumer_group_messages SET status='dead',worker='',token_hash=NULL,lease_until=NULL,credential_id=NULL,
 failure_code=CASE WHEN deadline_invalid THEN 'invalid_delivery_deadline' ELSE 'delivery_expired' END
 WHERE group_id=sqlc.arg(group_id)::text::uuid AND status<>'dead'
 AND (deadline_invalid OR delivery_deadline<=clock_timestamp());

-- name: ReplayDeadlineRejected :one
SELECT EXISTS(SELECT 1 FROM managed_realtime_consumer_group_messages WHERE group_id=sqlc.arg(group_id)::text::uuid AND sequence=sqlc.arg(sequence)::bigint
 AND (deadline_invalid OR delivery_deadline<=clock_timestamp()))::boolean;

-- name: ReadClaimRate :one
WITH observed AS (SELECT clock_timestamp() AS now)
SELECT g.claim_rate_per_second,g.claim_rate_burst,g.throttled_claim_requests::text AS throttled_claim_requests,
 (CASE WHEN g.claim_rate_per_second=0 THEN 0 ELSE greatest(0,ceil(extract(epoch FROM
 (g.claim_rate_tat-make_interval(secs=>((g.claim_rate_burst-1)*ceil(1000000.0/nullif(g.claim_rate_per_second,0))/1000000.0)::double precision)-o.now))*1000)) END)::bigint AS retry_after_milliseconds
FROM managed_realtime_consumer_groups g CROSS JOIN observed o WHERE g.id=sqlc.arg(group_id)::text::uuid;

-- name: AdvanceClaimRate :exec
UPDATE managed_realtime_consumer_groups SET claim_rate_tat=greatest(clock_timestamp(),claim_rate_tat)
 +make_interval(secs=>(ceil(1000000.0/claim_rate_per_second)/1000000.0)::double precision)
 WHERE id=sqlc.arg(group_id)::text::uuid AND claim_rate_per_second>0;

-- name: CountClaimThrottle :exec
UPDATE managed_realtime_consumer_groups SET throttled_claim_requests=throttled_claim_requests+1 WHERE id=sqlc.arg(group_id)::text::uuid;

-- name: ConfigureClaimRate :exec
UPDATE managed_realtime_consumer_groups SET
 claim_rate_tat=CASE WHEN sqlc.arg(rate)::integer=0 OR claim_rate_per_second=0 THEN clock_timestamp()
 ELSE least(greatest(clock_timestamp(),claim_rate_tat),clock_timestamp()+make_interval(secs=>(sqlc.arg(burst)::integer*ceil(1000000.0/nullif(sqlc.arg(rate)::integer,0))/1000000.0)::double precision)) END,
 claim_rate_per_second=sqlc.arg(rate)::integer,claim_rate_burst=sqlc.arg(burst)::integer
 WHERE id=sqlc.arg(group_id)::text::uuid AND (claim_rate_per_second<>sqlc.arg(rate)::integer OR claim_rate_burst<>sqlc.arg(burst)::integer);

-- name: SkipFilteredMessage :exec
UPDATE managed_realtime_consumer_groups SET assigned_sequence=sqlc.arg(sequence)::bigint,filtered_messages=filtered_messages+1 WHERE id=sqlc.arg(group_id)::text::uuid;

-- name: SetInitialMessageFilter :exec
UPDATE managed_realtime_consumer_groups SET message_filter=sqlc.arg(message_filter)::jsonb WHERE id=sqlc.arg(group_id)::text::uuid;

-- name: ExpireWorkerHeartbeats :exec
UPDATE managed_realtime_worker_heartbeats h SET lost=true WHERE group_id=sqlc.arg(group_id)::text::uuid AND NOT lost
 AND (last_seen_at<=clock_timestamp()-make_interval(secs=>sqlc.arg(timeout_seconds)::double precision)
 OR (credential_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM managed_realtime_worker_credentials c WHERE c.id=h.credential_id AND c.group_id=h.group_id AND c.revoked_at IS NULL AND c.expires_at>clock_timestamp() AND 'heartbeat'=ANY(c.permissions))));

-- name: PruneWorkerHeartbeats :exec
DELETE FROM managed_realtime_worker_heartbeats WHERE group_id=sqlc.arg(group_id)::text::uuid AND lost AND last_seen_at<clock_timestamp()-make_interval(secs=>sqlc.arg(history_seconds)::double precision);

-- name: ReadWorkerHeartbeats :many
SELECT h.id::text AS id,h.worker,h.ready,h.status_code,h.last_seen_at,h.lost,
 (h.last_seen_at>clock_timestamp()-make_interval(secs=>sqlc.arg(timeout_seconds)::double precision)
 AND (h.credential_id IS NULL OR EXISTS(SELECT 1 FROM managed_realtime_worker_credentials c WHERE c.id=h.credential_id AND c.group_id=h.group_id AND c.revoked_at IS NULL AND c.expires_at>clock_timestamp() AND 'heartbeat'=ANY(c.permissions))))::boolean AS live
 FROM managed_realtime_worker_heartbeats h WHERE h.group_id=sqlc.arg(group_id)::text::uuid ORDER BY h.worker,h.id;

-- name: WriteWorkerHeartbeat :one
INSERT INTO managed_realtime_worker_heartbeats (id,group_id,worker,principal,credential_id,ready,status_code,last_seen_at,lost)
 VALUES(gen_random_uuid(),sqlc.arg(group_id)::text::uuid,sqlc.arg(worker)::text,sqlc.arg(principal)::text,sqlc.narg(credential_id)::uuid,sqlc.arg(ready)::boolean,sqlc.arg(status_code)::text,clock_timestamp(),false)
 ON CONFLICT(group_id,worker,principal) DO UPDATE SET ready=excluded.ready,status_code=excluded.status_code,last_seen_at=excluded.last_seen_at,lost=false
 RETURNING id::text,worker,ready,status_code,last_seen_at,lost;

-- name: HasWorkerHeartbeat :one
SELECT EXISTS(SELECT 1 FROM managed_realtime_worker_heartbeats WHERE group_id=sqlc.arg(group_id)::text::uuid AND worker=sqlc.arg(worker)::text AND principal=sqlc.arg(principal)::text)::boolean;

-- name: CountWorkerHeartbeats :one
SELECT count(*)::integer FROM managed_realtime_worker_heartbeats WHERE group_id=sqlc.arg(group_id)::text::uuid;

-- name: DueWorkerHealthGroups :many
SELECT g.id::text FROM managed_realtime_consumer_groups g JOIN managed_realtime_endpoints e ON e.id=g.endpoint_id JOIN accounts a ON a.id=e.account_id JOIN apps p ON p.id=e.app_id
 WHERE e.enabled AND a.status IN ('active','past_due') AND p.status<>'deleted' AND g.next_worker_health_check_at<=clock_timestamp()
 AND EXISTS(SELECT 1 FROM managed_realtime_worker_heartbeats h WHERE h.group_id=g.id)
 ORDER BY g.next_worker_health_check_at,g.id LIMIT sqlc.arg(row_limit)::integer;

-- name: LockWorkerHealthGroup :one
SELECT a.plan FROM managed_realtime_consumer_groups g JOIN managed_realtime_endpoints e ON e.id=g.endpoint_id JOIN accounts a ON a.id=e.account_id JOIN apps p ON p.id=e.app_id
 WHERE g.id=sqlc.arg(group_id)::text::uuid AND e.enabled AND a.status IN ('active','past_due') AND p.status<>'deleted' AND g.next_worker_health_check_at<=clock_timestamp()
 FOR UPDATE OF g SKIP LOCKED;

-- name: AdvanceWorkerHealthScan :exec
UPDATE managed_realtime_consumer_groups SET next_worker_health_check_at=clock_timestamp()+make_interval(secs=>sqlc.arg(scan_seconds)::double precision) WHERE id=sqlc.arg(group_id)::text::uuid;

-- name: EvictLostHeartbeat :execrows
DELETE FROM managed_realtime_worker_heartbeats WHERE id=(SELECT id FROM managed_realtime_worker_heartbeats WHERE group_id=sqlc.arg(group_id)::text::uuid AND lost ORDER BY last_seen_at,id LIMIT 1);

-- name: CompleteMeasuredMessage :one
WITH removed AS (
 DELETE FROM managed_realtime_consumer_group_messages WHERE group_id=sqlc.arg(group_id)::text::uuid AND sequence=sqlc.arg(sequence)::bigint
 AND token_hash=sqlc.arg(token_hash)::bytea AND status='leased' AND lease_until>clock_timestamp()
 RETURNING sequence,group_id,processing_started_at,fencing_token,attempts
), recorded AS (
 SELECT record_realtime_group_metric(group_id,0::bigint,1::bigint,0::bigint,0::bigint,0::numeric,
 greatest(0,extract(epoch FROM (clock_timestamp()-processing_started_at))*1000)::numeric) FROM removed WHERE processing_started_at IS NOT NULL
), circuit AS (SELECT observe_realtime_circuit(group_id,sequence,fencing_token,true,'success') FROM removed), history AS (SELECT record_realtime_delivery_history(group_id,sequence,'completed',attempts,fencing_token,'') FROM removed)
SELECT sequence FROM removed CROSS JOIN (SELECT count(*) FROM recorded) AS observed CROSS JOIN (SELECT count(*) FROM circuit) AS circuit_observed CROSS JOIN (SELECT count(*) FROM history) AS history_observed;

-- name: ReadGroupMetricBuckets :many
SELECT b.bucket_at::timestamptz AS bucket_at,
 coalesce(m.first_claims,0)::text AS first_claims,coalesce(m.completed,0)::text AS completed,
 coalesce(m.retries,0)::text AS retries,coalesce(m.dead_letters,0)::text AS dead_letters,
 CASE WHEN m.first_claims>0 THEN (m.queue_milliseconds/m.first_claims)::double precision ELSE 0::double precision END AS queue_wait_milliseconds,
 CASE WHEN m.completed>0 THEN (m.processing_milliseconds/m.completed)::double precision ELSE 0::double precision END AS processing_milliseconds
FROM generate_series(date_trunc('minute',statement_timestamp(),'UTC')-make_interval(mins=>sqlc.arg(bucket_count)::integer-1),date_trunc('minute',statement_timestamp(),'UTC'),interval '1 minute') AS b(bucket_at)
LEFT JOIN managed_realtime_group_metrics m ON m.group_id=sqlc.arg(group_id)::text::uuid AND m.bucket_at=b.bucket_at ORDER BY b.bucket_at;

-- name: ListInspectorChannelsWithSchedules :many
WITH channels AS (
 SELECT channel FROM managed_realtime_channel_heads WHERE endpoint_id=sqlc.arg(endpoint_id)::text::uuid
 UNION
 SELECT channel FROM managed_realtime_schedules WHERE endpoint_id=sqlc.arg(endpoint_id)::text::uuid AND (status IN ('pending','paused') OR updated_at>=statement_timestamp()-interval '24 hours')
)
SELECT c.channel,coalesce(h.oldest_sequence,1)::bigint AS oldest_sequence,coalesce(h.next_sequence-1,0)::bigint AS latest_sequence
FROM channels c LEFT JOIN managed_realtime_channel_heads h ON h.endpoint_id=sqlc.arg(endpoint_id)::text::uuid AND h.channel=c.channel
ORDER BY c.channel LIMIT sqlc.arg(row_limit)::integer;

-- name: ReadPriorityPolicy :one
SELECT priority_metadata_key,priority_turn FROM managed_realtime_consumer_groups WHERE id=sqlc.arg(group_id)::text::uuid;
-- name: SetInitialPriorityPolicy :exec
UPDATE managed_realtime_consumer_groups SET priority_metadata_key=sqlc.arg(metadata_key)::text WHERE id=sqlc.arg(group_id)::text::uuid;
-- name: FreezeMessagePriority :exec
UPDATE managed_realtime_consumer_group_messages SET priority=sqlc.arg(priority)::integer WHERE group_id=sqlc.arg(group_id)::text::uuid AND sequence=sqlc.arg(sequence)::bigint;
-- name: AdvancePriorityTurn :exec
UPDATE managed_realtime_consumer_groups SET priority_turn=(priority_turn+1)%sqlc.arg(fair_turns)::integer WHERE id=sqlc.arg(group_id)::text::uuid;
-- name: NextPriorityMessage :one
SELECT m.sequence FROM managed_realtime_consumer_group_messages m
 WHERE m.group_id=sqlc.arg(group_id)::text::uuid AND m.status='ready' AND m.available_at<=clock_timestamp() AND NOT m.deadline_invalid AND (m.delivery_deadline IS NULL OR m.delivery_deadline>clock_timestamp())
 AND (NOT sqlc.arg(ordered)::boolean OR NOT EXISTS(SELECT 1 FROM managed_realtime_consumer_group_messages b WHERE b.group_id=m.group_id AND b.ordering_key=m.ordering_key AND (b.status='leased' OR (b.sequence<m.sequence AND (b.status<>'dead' OR sqlc.arg(dead_policy)::text='block')))))
 ORDER BY CASE WHEN sqlc.arg(prefer_priority)::boolean THEN m.priority ELSE 0 END DESC,m.sequence LIMIT 1;
-- name: ReadPriorityBacklog :many
SELECT priority,count(*) FILTER(WHERE status='ready')::integer AS ready,count(*) FILTER(WHERE status='leased')::integer AS leased,count(*) FILTER(WHERE status='dead')::integer AS dead
FROM managed_realtime_consumer_group_messages WHERE group_id=sqlc.arg(group_id)::text::uuid GROUP BY priority ORDER BY priority DESC;

-- name: ReadSettlementLeaseAttempts :one
SELECT attempts FROM managed_realtime_consumer_group_messages WHERE group_id=sqlc.arg(group_id)::text::uuid AND sequence=sqlc.arg(sequence)::bigint
 AND status='leased' AND token_hash=sqlc.arg(token_hash)::bytea AND lease_until>clock_timestamp()
 AND (sqlc.narg(credential_id)::text::uuid IS NULL OR credential_id=sqlc.narg(credential_id)::text::uuid);
-- name: AuthorizeBatchSettlementActions :one
SELECT EXISTS(SELECT 1 FROM managed_realtime_worker_credentials WHERE id=sqlc.arg(credential_id)::text::uuid AND group_id=sqlc.arg(group_id)::text::uuid
 AND token_hash=sqlc.arg(token_hash)::bytea AND revoked_at IS NULL AND expires_at>clock_timestamp() AND sqlc.arg(actions)::text[]<@permissions)::boolean;

-- name: ReadCircuitStatus :one
SELECT circuit_enabled,circuit_threshold,circuit_cooldown_seconds,circuit_failures,circuit_state,circuit_open_until,circuit_probe_sequence,circuit_reason,
 (circuit_enabled AND (circuit_state='open' OR circuit_probe_sequence IS NOT NULL))::boolean AS blocked,
 CASE WHEN circuit_state='open' THEN greatest(1,ceil(extract(epoch FROM (circuit_open_until-clock_timestamp()))*1000))::bigint
 WHEN circuit_probe_sequence IS NOT NULL THEN sqlc.arg(probe_poll_milliseconds)::bigint ELSE 0::bigint END AS retry_after_milliseconds
 FROM managed_realtime_consumer_groups g WHERE id=sqlc.arg(group_id)::text::uuid;
-- name: ConfigureCircuitPolicy :exec
UPDATE managed_realtime_consumer_groups SET circuit_enabled=sqlc.arg(enabled)::boolean,circuit_threshold=sqlc.arg(threshold)::integer,circuit_cooldown_seconds=sqlc.arg(cooldown_seconds)::integer,
 circuit_state='closed',circuit_failures=0,circuit_open_until=NULL,circuit_probe_sequence=NULL,circuit_probe_fencing_token=NULL,circuit_reason='configured'
 WHERE id=sqlc.arg(group_id)::text::uuid AND (circuit_enabled,circuit_threshold,circuit_cooldown_seconds) IS DISTINCT FROM (sqlc.arg(enabled)::boolean,sqlc.arg(threshold)::integer,sqlc.arg(cooldown_seconds)::integer);
-- name: RefreshCircuitGate :exec
UPDATE managed_realtime_consumer_groups g SET
 circuit_state=CASE WHEN circuit_state='open' THEN 'half_open' ELSE 'open' END,
 circuit_open_until=CASE WHEN circuit_state='open' THEN NULL ELSE clock_timestamp()+make_interval(secs=>circuit_cooldown_seconds) END,
 circuit_probe_sequence=NULL,circuit_probe_fencing_token=NULL,circuit_reason=CASE WHEN circuit_state='open' THEN 'cooldown_elapsed' ELSE 'probe_missing' END
 WHERE id=sqlc.arg(group_id)::text::uuid AND circuit_enabled AND
 ((circuit_state='open' AND circuit_open_until<=clock_timestamp()) OR (circuit_state='half_open' AND circuit_probe_sequence IS NOT NULL AND NOT EXISTS(SELECT 1 FROM managed_realtime_consumer_group_messages m WHERE m.group_id=g.id AND m.sequence=g.circuit_probe_sequence AND m.fencing_token=g.circuit_probe_fencing_token AND m.status='leased' AND m.lease_until>clock_timestamp())));
-- name: RegisterCircuitProbe :exec
UPDATE managed_realtime_consumer_groups SET circuit_probe_sequence=sqlc.arg(sequence)::bigint,circuit_probe_fencing_token=sqlc.arg(fencing_token)::text::bigint
 WHERE id=sqlc.arg(group_id)::text::uuid AND circuit_enabled AND circuit_state='half_open' AND circuit_probe_sequence IS NULL;

-- name: ReadDeliveryAttemptHistory :many
SELECT id::text AS id,sequence::text AS sequence,event,attempt,coalesce(fencing_token::text,'')::text AS fencing_token,failure_code,recorded_at
 FROM managed_realtime_group_delivery_history WHERE group_id=sqlc.arg(group_id)::text::uuid AND sequence=sqlc.arg(sequence)::bigint AND id>sqlc.arg(after_id)::bigint
 AND recorded_at>=transaction_timestamp()-make_interval(secs=>sqlc.arg(retention_seconds)::double precision)
 ORDER BY id LIMIT sqlc.arg(row_limit)::integer;
-- name: ReadPrunedDeliveryEvents :one
SELECT history_pruned_events::text FROM managed_realtime_consumer_groups WHERE id=sqlc.arg(group_id)::text::uuid;

-- name: PruneExpiredDeliveryHistory :exec
WITH removed AS (DELETE FROM managed_realtime_group_delivery_history WHERE group_id=sqlc.arg(group_id)::text::uuid AND recorded_at<transaction_timestamp()-make_interval(secs=>sqlc.arg(retention_seconds)::double precision) RETURNING id)
UPDATE managed_realtime_consumer_groups SET history_pruned_events=history_pruned_events+(SELECT count(*) FROM removed) WHERE id=sqlc.arg(group_id)::text::uuid AND EXISTS(SELECT 1 FROM removed);

-- name: ReleaseGroupMessage :one
UPDATE managed_realtime_consumer_group_messages SET status='ready',attempts=greatest(0,attempts-1),release_count=release_count+1,
 worker='',token_hash=NULL,lease_until=NULL,credential_id=NULL,failure_code='',available_at=clock_timestamp()
 WHERE group_id=sqlc.arg(group_id)::text::uuid AND sequence=sqlc.arg(sequence)::bigint AND status='leased'
 AND token_hash=sqlc.arg(token_hash)::bytea AND lease_until>clock_timestamp() RETURNING available_at;

-- name: PruneSettlementReceipts :exec
DELETE FROM managed_realtime_group_settlement_receipts WHERE group_id=sqlc.arg(group_id)::text::uuid AND expires_at<=clock_timestamp();
-- name: ReadSettlementReceipt :one
SELECT request_hash,result FROM managed_realtime_group_settlement_receipts WHERE group_id=sqlc.arg(group_id)::text::uuid
 AND principal=sqlc.arg(principal)::text AND idempotency_key=sqlc.arg(idempotency_key)::text::uuid AND expires_at>clock_timestamp();
-- name: CountSettlementReceipts :one
SELECT count(*)::integer FROM managed_realtime_group_settlement_receipts WHERE group_id=sqlc.arg(group_id)::text::uuid;
-- name: SaveSettlementReceipt :exec
INSERT INTO managed_realtime_group_settlement_receipts(group_id,principal,idempotency_key,request_hash,result,expires_at)
 VALUES(sqlc.arg(group_id)::text::uuid,sqlc.arg(principal)::text,sqlc.arg(idempotency_key)::text::uuid,sqlc.arg(request_hash)::bytea,sqlc.arg(result)::jsonb,
 clock_timestamp()+make_interval(secs=>sqlc.arg(retention_seconds)::double precision));

-- name: SetInitialDeduplicationPolicy :exec
UPDATE managed_realtime_consumer_groups SET deduplication_metadata_key=sqlc.arg(metadata_key)::text,deduplication_window_seconds=sqlc.arg(window_seconds)::integer WHERE id=sqlc.arg(group_id)::text::uuid;
-- name: ReadDeduplicationPolicy :one
SELECT deduplication_metadata_key,deduplication_window_seconds FROM managed_realtime_consumer_groups WHERE id=sqlc.arg(group_id)::text::uuid;
-- name: PruneExpiredEventIDs :exec
DELETE FROM managed_realtime_group_event_ids WHERE group_id=sqlc.arg(group_id)::text::uuid AND expires_at<=clock_timestamp();
-- name: EventIDRemembered :one
SELECT EXISTS(SELECT 1 FROM managed_realtime_group_event_ids WHERE group_id=sqlc.arg(group_id)::text::uuid AND event_hash=sqlc.arg(event_hash)::bytea AND expires_at>clock_timestamp())::boolean;
-- name: ReadEventIDCapacity :one
SELECT count(*)::integer AS key_count,coalesce(greatest(1,ceil(extract(epoch FROM (min(expires_at)-clock_timestamp()))*1000)),0)::bigint AS retry_after_milliseconds
 FROM managed_realtime_group_event_ids WHERE group_id=sqlc.arg(group_id)::text::uuid;
-- name: RememberEventID :exec
INSERT INTO managed_realtime_group_event_ids(group_id,event_hash,expires_at) VALUES(sqlc.arg(group_id)::text::uuid,sqlc.arg(event_hash)::bytea,
 clock_timestamp()+make_interval(secs=>sqlc.arg(window_seconds)::double precision))
 ON CONFLICT(group_id,event_hash) DO UPDATE SET expires_at=excluded.expires_at WHERE managed_realtime_group_event_ids.expires_at<=clock_timestamp();
-- name: SkipDuplicateEvent :exec
UPDATE managed_realtime_consumer_groups SET assigned_sequence=sqlc.arg(sequence)::bigint,duplicate_messages=duplicate_messages+1 WHERE id=sqlc.arg(group_id)::text::uuid;
-- name: ReadGroupDeduplicationStatuses :many
SELECT g.id::text AS group_id,g.deduplication_metadata_key,g.deduplication_window_seconds,g.duplicate_messages::text AS duplicate_messages,
 (SELECT count(*) FROM managed_realtime_group_event_ids k WHERE k.group_id=g.id AND k.expires_at>statement_timestamp())::integer AS live_keys
 FROM managed_realtime_consumer_groups g WHERE g.id=ANY(sqlc.arg(group_ids)::text[]::uuid[]);

-- name: ReadDeadLetterExportGroup :one
SELECT id::text AS group_id,assigned_sequence,dead_letter_retention_seconds,transaction_timestamp()::timestamptz AS observed_at
 FROM managed_realtime_consumer_groups WHERE endpoint_id=sqlc.arg(endpoint_id)::text::uuid AND channel=sqlc.arg(channel)::text AND name=sqlc.arg(group_name)::text;
-- name: ReadDeadLetterExportRows :many
SELECT sequence::text AS sequence,message,dead_since,attempts,failure_code,coalesce(fencing_token::text,'')::text AS fencing_token,
 delivery_deadline,deadline_invalid,ordering_key,priority,
 (CASE WHEN sqlc.arg(retention_seconds)::integer>0 THEN dead_since+make_interval(secs=>sqlc.arg(retention_seconds)::double precision) END)::timestamptz AS expires_at
 FROM managed_realtime_consumer_group_messages WHERE group_id=sqlc.arg(group_id)::text::uuid AND status='dead'
 AND sequence>sqlc.arg(after_sequence)::bigint AND sequence<=sqlc.arg(through_sequence)::bigint AND dead_since<=sqlc.arg(snapshot_at)::timestamptz
 AND (cardinality(sqlc.arg(failure_codes)::text[])=0 OR failure_code=ANY(sqlc.arg(failure_codes)::text[]))
 ORDER BY sequence LIMIT sqlc.arg(row_limit)::integer;

-- name: ListReplayJobs :many
SELECT * FROM managed_realtime_replay_jobs WHERE group_id=sqlc.arg(group_id)::uuid ORDER BY (state IN ('running','paused','checking','check_paused')) DESC,created_at DESC,id LIMIT sqlc.arg(row_limit);
-- name: PruneReplayJobs :exec
DELETE FROM managed_realtime_replay_jobs WHERE group_id=sqlc.arg(group_id)::uuid AND state IN ('completed','cancelled') AND id NOT IN (SELECT id FROM managed_realtime_replay_jobs WHERE group_id=sqlc.arg(group_id)::uuid AND state IN ('completed','cancelled') ORDER BY created_at DESC,id LIMIT sqlc.arg(retain_count));
-- name: CountActiveReplayJobs :one
SELECT count(*) FROM managed_realtime_replay_jobs WHERE group_id=sqlc.arg(group_id)::uuid AND state IN ('running','paused','checking','check_paused');
-- name: CreateReplayJob :one
INSERT INTO managed_realtime_replay_jobs(id,group_id,state,rate,selection,dry_run,reviewed_job_id) VALUES(sqlc.arg(id)::uuid,sqlc.arg(group_id)::uuid,CASE WHEN sqlc.arg(dry_run)::boolean THEN 'checking' ELSE 'running' END,sqlc.arg(rate),sqlc.arg(selection),sqlc.arg(dry_run),sqlc.narg(reviewed_job_id)::uuid) RETURNING *;
-- name: ReadReplayJob :one
SELECT * FROM managed_realtime_replay_jobs WHERE group_id=sqlc.arg(group_id)::uuid AND id=sqlc.arg(id)::uuid FOR UPDATE;
-- name: UpdateReplayJob :one
UPDATE managed_realtime_replay_jobs SET state=sqlc.arg(state),position=sqlc.arg(position),outcomes=sqlc.arg(outcomes),next_at=clock_timestamp()+make_interval(secs=>sqlc.arg(delay)::double precision),updated_at=clock_timestamp() WHERE group_id=sqlc.arg(group_id)::uuid AND id=sqlc.arg(id)::uuid RETURNING *;
-- name: DueReplayJobs :many
SELECT j.id,j.group_id,g.endpoint_id,g.channel,g.name FROM managed_realtime_replay_jobs j JOIN managed_realtime_consumer_groups g ON g.id=j.group_id JOIN managed_realtime_endpoints e ON e.id=g.endpoint_id JOIN accounts a ON a.id=e.account_id JOIN apps p ON p.id=e.app_id WHERE j.state IN ('running','checking') AND j.next_at<=clock_timestamp() AND e.enabled AND a.status IN ('active','past_due') AND p.status<>'deleted' ORDER BY j.next_at,j.id LIMIT sqlc.arg(row_limit);
-- name: ReplayJobEligible :one
SELECT EXISTS(SELECT 1 FROM managed_realtime_replay_jobs j JOIN managed_realtime_consumer_groups g ON g.id=j.group_id JOIN managed_realtime_endpoints e ON e.id=g.endpoint_id JOIN accounts a ON a.id=e.account_id JOIN apps p ON p.id=e.app_id WHERE j.id=sqlc.arg(id)::uuid AND j.state IN ('running','checking') AND j.next_at<=clock_timestamp() AND e.enabled AND a.status IN ('active','past_due') AND p.status<>'deleted');

-- name: RecordReplayJobError :exec
UPDATE managed_realtime_replay_jobs SET error_count=error_count+1,last_error='processing_failed',updated_at=clock_timestamp(),next_at=clock_timestamp()+make_interval(secs=>sqlc.arg(delay)::double precision) WHERE id=sqlc.arg(id)::uuid AND group_id=sqlc.arg(group_id)::uuid AND state IN ('running','checking');

-- name: HasActiveOrderingLease :one
SELECT EXISTS(SELECT 1 FROM managed_realtime_consumer_group_messages WHERE group_id=sqlc.arg(group_id)::text::uuid AND ordering_key=sqlc.arg(ordering_key)::text AND status='leased' AND lease_until>clock_timestamp())::boolean;
-- name: MarkReviewedReplayStarted :exec
UPDATE managed_realtime_replay_jobs SET started_job_id=sqlc.arg(started_job_id)::uuid,updated_at=clock_timestamp() WHERE group_id=sqlc.arg(group_id)::uuid AND id=sqlc.arg(id)::uuid;

-- name: PruneReplayJobsExcept :exec
DELETE FROM managed_realtime_replay_jobs WHERE group_id=sqlc.arg(group_id)::uuid AND state IN ('completed','cancelled') AND id<>sqlc.arg(keep_id)::uuid AND id NOT IN (SELECT id FROM managed_realtime_replay_jobs WHERE group_id=sqlc.arg(group_id)::uuid AND state IN ('completed','cancelled') AND id<>sqlc.arg(keep_id)::uuid ORDER BY created_at DESC,id LIMIT sqlc.arg(retain_count));

-- name: RecordRealtimeUsage :execrows
-- Cumulative per-process values only grow, so taking the larger value makes
-- repeated and out-of-order reports harmless. Unknown endpoints record nothing.
INSERT INTO realtime_usage_hours AS t (endpoint_id,hour,node_name,incarnation,account_id,app_id,minute_peak_connections,
 connection_seconds,delivered_messages,delivered_bytes,received_messages,received_bytes,reported_at)
SELECT e.id,sqlc.arg(hour)::timestamptz,sqlc.arg(node_name)::text,sqlc.arg(incarnation)::uuid,e.account_id,e.app_id,
 sqlc.arg(minute_peak_connections)::integer[],sqlc.arg(connection_seconds)::bigint,sqlc.arg(delivered_messages)::bigint,
 sqlc.arg(delivered_bytes)::bigint,sqlc.arg(received_messages)::bigint,sqlc.arg(received_bytes)::bigint,clock_timestamp()
FROM managed_realtime_endpoints e WHERE e.id=sqlc.arg(endpoint_id)::uuid
ON CONFLICT (endpoint_id,hour,node_name,incarnation) DO UPDATE SET
 minute_peak_connections=ARRAY(SELECT greatest(t.minute_peak_connections[g.i],excluded.minute_peak_connections[g.i]) FROM generate_series(1,60) AS g(i) ORDER BY g.i),
 connection_seconds=greatest(t.connection_seconds,excluded.connection_seconds),
 delivered_messages=greatest(t.delivered_messages,excluded.delivered_messages),
 delivered_bytes=greatest(t.delivered_bytes,excluded.delivered_bytes),
 received_messages=greatest(t.received_messages,excluded.received_messages),
 received_bytes=greatest(t.received_bytes,excluded.received_bytes),
 reported_at=excluded.reported_at;

-- name: ReadRealtimeUsageHours :many
-- Fleet usage per endpoint and hour. The peak is the largest per-minute sum of
-- node peaks, so connections on different nodes at the same minute add up.
WITH selected AS (
 SELECT * FROM realtime_usage_hours WHERE account_id=sqlc.arg(account_id)::uuid
 AND hour>=sqlc.arg(from_hour)::timestamptz AND hour<sqlc.arg(to_hour)::timestamptz),
minutes AS (
 SELECT s.endpoint_id,s.hour,m.i,sum(m.v)::bigint AS connections
 FROM selected s CROSS JOIN LATERAL unnest(s.minute_peak_connections) WITH ORDINALITY AS m(v,i)
 GROUP BY s.endpoint_id,s.hour,m.i),
peaks AS (SELECT endpoint_id,hour,max(connections)::bigint AS peak_connections FROM minutes GROUP BY endpoint_id,hour)
SELECT s.endpoint_id::text AS endpoint_id,max(s.app_id::text)::text AS app_id,s.hour,p.peak_connections,
 sum(s.connection_seconds)::bigint AS connection_seconds,sum(s.delivered_messages)::bigint AS delivered_messages,
 sum(s.delivered_bytes)::bigint AS delivered_bytes,sum(s.received_messages)::bigint AS received_messages,
 sum(s.received_bytes)::bigint AS received_bytes
FROM selected s JOIN peaks p ON p.endpoint_id=s.endpoint_id AND p.hour=s.hour
GROUP BY s.endpoint_id,s.hour,p.peak_connections
ORDER BY s.hour,s.endpoint_id
LIMIT sqlc.arg(row_limit)::integer;

-- name: MarkNodeConnectionSync :exec
INSERT INTO realtime_node_connection_syncs (node_name,synced_at) VALUES (sqlc.arg(node_name)::text,clock_timestamp())
ON CONFLICT (node_name) DO UPDATE SET synced_at=excluded.synced_at;

-- name: ForgetSilentConnectionNodes :exec
DELETE FROM realtime_node_connection_syncs WHERE synced_at<clock_timestamp()-make_interval(secs=>sqlc.arg(silent_seconds)::double precision);

-- name: ClearNodeConnectionsExcept :exec
DELETE FROM realtime_node_connections WHERE node_name=sqlc.arg(node_name)::text AND NOT (account_id=ANY(sqlc.arg(account_ids)::uuid[]));

-- name: UpsertNodeConnections :exec
-- Writes only the counts that changed since the node's previous sync.
INSERT INTO realtime_node_connections AS t (node_name,account_id,connections)
SELECT sqlc.arg(node_name)::text,(sqlc.arg(account_ids)::uuid[])[g.i],(sqlc.arg(counts)::integer[])[g.i]
FROM generate_subscripts(sqlc.arg(account_ids)::uuid[],1) AS g(i)
ON CONFLICT (node_name,account_id) DO UPDATE SET connections=excluded.connections
WHERE t.connections<>excluded.connections;

-- name: ReadRemoteNodeConnections :many
-- Other nodes' fresh counts, largest accounts first.
SELECT c.account_id::text AS account_id,sum(c.connections)::bigint AS connections
FROM realtime_node_connections c JOIN realtime_node_connection_syncs s ON s.node_name=c.node_name
WHERE c.node_name<>sqlc.arg(node_name)::text
 AND s.synced_at>clock_timestamp()-make_interval(secs=>sqlc.arg(stale_seconds)::double precision)
GROUP BY c.account_id ORDER BY sum(c.connections) DESC,c.account_id
LIMIT sqlc.arg(row_limit)::integer;

-- name: ReadAccountOpenConnections :one
-- An account's open connections across nodes with a fresh sync (ADR-1323).
SELECT coalesce(sum(c.connections),0)::bigint AS connections
FROM realtime_node_connections c JOIN realtime_node_connection_syncs s ON s.node_name=c.node_name
WHERE c.account_id=sqlc.arg(account_id)::uuid
 AND s.synced_at>clock_timestamp()-make_interval(secs=>sqlc.arg(stale_seconds)::double precision);

-- name: ReadRealtimeAccountUsageHours :many
-- Account usage per hour across all endpoints and nodes. The peak is the
-- largest per-minute sum, so it never double-counts across endpoints.
WITH selected AS (
 SELECT * FROM realtime_usage_hours WHERE account_id=sqlc.arg(account_id)::uuid
 AND hour>=sqlc.arg(from_hour)::timestamptz AND hour<sqlc.arg(to_hour)::timestamptz),
minutes AS (
 SELECT s.hour,m.i,sum(m.v)::bigint AS connections
 FROM selected s CROSS JOIN LATERAL unnest(s.minute_peak_connections) WITH ORDINALITY AS m(v,i)
 GROUP BY s.hour,m.i),
peaks AS (SELECT hour,max(connections)::bigint AS peak_connections FROM minutes GROUP BY hour)
SELECT s.hour,p.peak_connections,
 sum(s.connection_seconds)::bigint AS connection_seconds,sum(s.delivered_messages)::bigint AS delivered_messages,
 sum(s.delivered_bytes)::bigint AS delivered_bytes,sum(s.received_messages)::bigint AS received_messages,
 sum(s.received_bytes)::bigint AS received_bytes
FROM selected s JOIN peaks p ON p.hour=s.hour
GROUP BY s.hour,p.peak_connections
ORDER BY s.hour;
