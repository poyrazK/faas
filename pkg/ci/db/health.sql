-- name: ClaimCIWorkerHealthSlot :one
WITH owned AS (
 SELECT slot FROM ci_worker_health WHERE worker=sqlc.arg(worker)::text AND token=sqlc.arg(token)::uuid
 AND heartbeat_at>clock_timestamp()-make_interval(secs=>sqlc.arg(expiry_seconds)::integer)
), candidate AS (
 SELECT n FROM generate_series(0,sqlc.arg(max_slots)::integer-1) n
 LEFT JOIN ci_worker_health h ON h.worker=sqlc.arg(worker)::text AND h.slot=n
 WHERE NOT EXISTS(SELECT 1 FROM owned) AND (h.heartbeat_at IS NULL OR h.heartbeat_at<=clock_timestamp()-make_interval(secs=>sqlc.arg(expiry_seconds)::integer))
 ORDER BY n LIMIT 1
), claimed AS (
INSERT INTO ci_worker_health(worker,slot,token,enabled,ready,cycle_sequence)
SELECT sqlc.arg(worker),n,sqlc.arg(token),sqlc.arg(enabled),sqlc.arg(ready),sqlc.arg(sequence)::bigint FROM candidate
ON CONFLICT(worker,slot) DO UPDATE SET token=excluded.token,enabled=excluded.enabled,ready=excluded.ready,
 started_at=clock_timestamp(),heartbeat_at=clock_timestamp(),cycle_at=NULL,cycle_sequence=excluded.cycle_sequence,success_at=NULL,last_error=''
WHERE ci_worker_health.heartbeat_at<=clock_timestamp()-make_interval(secs=>sqlc.arg(expiry_seconds)::integer)
RETURNING slot
)
SELECT slot FROM owned UNION ALL SELECT slot FROM claimed;

-- name: PulseCIWorkerHealth :execrows
UPDATE ci_worker_health SET heartbeat_at=clock_timestamp(),
 cycle_at=CASE WHEN sqlc.arg(sequence)::bigint>cycle_sequence THEN clock_timestamp() ELSE cycle_at END,
 success_at=CASE WHEN sqlc.arg(sequence)::bigint>cycle_sequence AND NOT sqlc.arg(failed)::boolean THEN clock_timestamp() ELSE success_at END,
 last_error=CASE WHEN sqlc.arg(sequence)::bigint<=cycle_sequence THEN last_error WHEN sqlc.arg(failed)::boolean THEN 'unavailable' ELSE '' END,
 cycle_sequence=greatest(cycle_sequence,sqlc.arg(sequence)::bigint)
WHERE worker=$1 AND slot=$2 AND token=$3
 AND heartbeat_at>clock_timestamp()-make_interval(secs=>sqlc.arg(expiry_seconds)::integer);

-- name: CIHealthWorkers :many
SELECT * FROM ci_worker_health ORDER BY worker,slot;

-- name: CIHealthNow :one
SELECT transaction_timestamp()::timestamptz AS now_at;

-- All ages are ages of the oldest retained pending item, not time spent in its
-- current state. Due means time eligible; authority/native/resource checks may
-- still prevent a claim. No row locks, JSON decoding, or provider calls.
-- name: CIHealthQueues :many
WITH work AS (
 SELECT 'executions'::text AS queue,
  (lease_until IS NULL OR lease_until<=now()) AS pending,lease_until>now() AS inflight,
  false AS dead,next_reconcile_at<=now() AND (lease_until IS NULL OR lease_until<=now()) AS due,
  lease_until<=now() AS stale,created_at
 FROM ci_executions WHERE mode='jobs' AND archived_at IS NULL AND status IN ('queued','running','cancelling')
  AND (sqlc.arg(fleet)::boolean OR account_id=sqlc.arg(account_id)::uuid)
 UNION ALL
 SELECT 'metadata_admission',status='pending',status='processing',status='dead',
  (status='pending' AND next_attempt_at<=now()) OR (status='processing' AND lease_until<=now()),
  status='processing' AND lease_until<=now(),created_at
 FROM ci_github_triggers WHERE sqlc.arg(fleet)::boolean OR account_id=sqlc.arg(account_id)::uuid
 UNION ALL
 SELECT 'automation_admission',status='pending',status='processing',status='dead',
  (status='pending' AND next_attempt_at<=now()) OR (status='processing' AND lease_until<=now()),
  status='processing' AND lease_until<=now(),created_at
 FROM ci_automation_events WHERE sqlc.arg(fleet)::boolean OR account_id=sqlc.arg(account_id)::uuid
 UNION ALL
 SELECT 'metadata_checks',status='pending',status='sending',status='dead',
  (status='pending' AND next_attempt_at<=now()) OR (status='sending' AND lease_until<=now()),
  status='sending' AND lease_until<=now(),created_at
 FROM ci_github_checks WHERE sqlc.arg(fleet)::boolean OR account_id=sqlc.arg(account_id)::uuid
 UNION ALL
 SELECT 'execution_checks',c.status='pending' OR (c.status='sent' AND e.version>c.delivered_version),
  c.status='sending',c.status='dead',
  (c.status='pending' AND c.next_attempt_at<=now()) OR (c.status='sending' AND c.lease_until<=now())
   OR (c.status='sent' AND e.version>c.delivered_version),
  c.status='sending' AND c.lease_until<=now(),c.created_at
 FROM ci_execution_checks c JOIN ci_executions e ON e.id=c.execution_id AND e.account_id=c.account_id
 WHERE e.archived_at IS NULL AND (sqlc.arg(fleet)::boolean OR c.account_id=sqlc.arg(account_id)::uuid)
 UNION ALL
 SELECT 'execution_checks',status='pending',status='sending',status='dead',
  (status='pending' AND next_attempt_at<=now()) OR (status='sending' AND lease_until<=now()),
  status='sending' AND lease_until<=now(),created_at
 FROM ci_automation_path_checks WHERE sqlc.arg(fleet)::boolean OR account_id=sqlc.arg(account_id)::uuid
 UNION ALL
 SELECT 'check_retries',true,false,false,true,false,requested_at
 FROM ci_execution_check_retries WHERE applied_at IS NULL AND (sqlc.arg(fleet)::boolean OR account_id=sqlc.arg(account_id)::uuid)
 UNION ALL
 SELECT 'check_retries',true,false,false,true,false,requested_at
 FROM ci_source_check_retries WHERE applied_at IS NULL AND (sqlc.arg(fleet)::boolean OR account_id=sqlc.arg(account_id)::uuid)
 UNION ALL
 SELECT 'retirement',true,op_until>now(),false,op_until IS NULL OR op_until<=now(),op_until<=now(),created_at
 FROM ci_managed_objects WHERE state!='deleted' AND (state='deleting' OR (state='ready' AND expires_at<=now())
  OR (state='pending' AND created_at<=now()-make_interval(hours=>sqlc.arg(orphan_hours)::integer)))
  AND (sqlc.arg(fleet)::boolean OR account_id=sqlc.arg(account_id)::uuid)
), queues AS (SELECT unnest(ARRAY['executions','metadata_admission','automation_admission','metadata_checks','execution_checks','check_retries','retirement'])::text AS queue)
SELECT q.queue::text,count(*) FILTER(WHERE w.pending)::bigint AS pending,
 count(*) FILTER(WHERE w.inflight)::bigint AS inflight,count(*) FILTER(WHERE w.dead)::bigint AS dead,
 count(*) FILTER(WHERE w.due)::bigint AS due,count(*) FILTER(WHERE w.stale)::bigint AS stale_leases,
 coalesce(greatest(0,extract(epoch FROM now()-min(w.created_at) FILTER(WHERE w.pending OR w.inflight)))::bigint,0)::bigint AS oldest_item_age_seconds
FROM queues q LEFT JOIN work w ON w.queue=q.queue GROUP BY q.queue ORDER BY q.queue;

-- name: CIHealthUsage :many
SELECT 'execution_details'::text AS resource,count(*)::bigint AS used FROM ci_executions
 WHERE mode='jobs' AND archived_at IS NULL AND (sqlc.arg(fleet)::boolean OR account_id=sqlc.arg(account_id)::uuid)
UNION ALL SELECT 'active_executions',count(*)::bigint FROM ci_executions
 WHERE mode='jobs' AND status IN ('queued','running','cancelling') AND (sqlc.arg(fleet)::boolean OR account_id=sqlc.arg(account_id)::uuid)
UNION ALL SELECT 'dispatch_leases',count(*)::bigint FROM ci_executions
 WHERE mode='jobs' AND lease_until>now() AND (sqlc.arg(fleet)::boolean OR account_id=sqlc.arg(account_id)::uuid)
UNION ALL SELECT 'archives',count(*)::bigint FROM ci_execution_archives WHERE sqlc.arg(fleet)::boolean OR account_id=sqlc.arg(account_id)::uuid
UNION ALL SELECT 'profiles',count(*)::bigint FROM ci_execution_profiles WHERE sqlc.arg(fleet)::boolean OR account_id=sqlc.arg(account_id)::uuid
UNION ALL SELECT 'revisions',count(*)::bigint FROM ci_pipeline_revisions WHERE sqlc.arg(fleet)::boolean OR account_id=sqlc.arg(account_id)::uuid
UNION ALL SELECT 'metadata_runs',count(*)::bigint FROM ci_runs WHERE sqlc.arg(fleet)::boolean OR account_id=sqlc.arg(account_id)::uuid
UNION ALL SELECT 'source_events',count(*)::bigint FROM (
 SELECT account_id FROM ci_github_triggers UNION ALL SELECT account_id FROM ci_automation_events
) events WHERE sqlc.arg(fleet)::boolean OR account_id=sqlc.arg(account_id)::uuid
UNION ALL SELECT 'storage_objects',count(*)::bigint FROM ci_managed_objects WHERE state!='deleted' AND (sqlc.arg(fleet)::boolean OR account_id=sqlc.arg(account_id)::uuid)
UNION ALL SELECT 'storage_bytes',coalesce(sum(bytes),0)::bigint FROM ci_managed_objects WHERE state!='deleted' AND (sqlc.arg(fleet)::boolean OR account_id=sqlc.arg(account_id)::uuid)
UNION ALL SELECT 'storage_journal',count(*)::bigint FROM ci_managed_objects WHERE sqlc.arg(fleet)::boolean OR account_id=sqlc.arg(account_id)::uuid
UNION ALL SELECT 'storage_io',count(*)::bigint FROM ci_managed_objects WHERE op_until>now() AND (sqlc.arg(fleet)::boolean OR account_id=sqlc.arg(account_id)::uuid)
ORDER BY resource;
