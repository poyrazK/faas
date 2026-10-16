-- name: ReadEnvironmentCacheIdentity :one
SELECT e.id::text AS environment_id,e.project_id::text AS project_id
FROM apps a JOIN project_environments e ON e.account_id=a.account_id AND e.project_id=a.project_id
WHERE a.account_id=sqlc.arg(account_id)::uuid AND a.id=sqlc.arg(app_id)::uuid AND a.status<>'deleted' AND e.slug=sqlc.arg(scope)::text;

-- name: LockEnvironmentCacheIdentity :one
SELECT e.id::text AS environment_id,e.project_id::text AS project_id
FROM apps a JOIN project_environments e ON e.account_id=a.account_id AND e.project_id=a.project_id
WHERE a.account_id=sqlc.arg(account_id)::uuid AND a.id=sqlc.arg(app_id)::uuid AND a.status<>'deleted' AND e.slug=sqlc.arg(scope)::text FOR UPDATE OF a FOR SHARE OF e;

-- name: ReadEnvironmentCachePolicy :one
SELECT to_jsonb(p) FROM project_environment_cache_policies p
WHERE p.account_id=sqlc.arg(account_id)::uuid AND p.app_id=sqlc.arg(app_id)::uuid AND p.environment_id=sqlc.arg(environment_id)::uuid;

-- name: WriteEnvironmentCachePolicy :exec
INSERT INTO project_environment_cache_policies(account_id,app_id,project_id,environment_id,revision,rules,inherited)
VALUES(sqlc.arg(account_id)::uuid,sqlc.arg(app_id)::uuid,sqlc.arg(project_id)::uuid,sqlc.arg(environment_id)::uuid,sqlc.arg(revision)::bigint,sqlc.arg(rules)::jsonb,sqlc.arg(inherited)::boolean)
ON CONFLICT(app_id,environment_id) DO UPDATE SET rules=excluded.rules,inherited=excluded.inherited,revision=excluded.revision,updated_at=clock_timestamp();

-- name: ReadEnvironmentCacheReceipt :one
SELECT request_hash,response FROM environment_cache_policy_receipts WHERE account_id=sqlc.arg(account_id)::uuid AND request_id=sqlc.arg(request_id)::uuid;

-- name: WriteEnvironmentCacheReceipt :exec
INSERT INTO environment_cache_policy_receipts(account_id,request_id,app_id,environment_id,request_hash,response)
VALUES(sqlc.arg(account_id)::uuid,sqlc.arg(request_id)::uuid,sqlc.arg(app_id)::uuid,sqlc.arg(environment_id)::uuid,sqlc.arg(request_hash)::text,sqlc.arg(response)::jsonb);

-- name: BeginEnvironmentCacheGatewaySession :execrows
INSERT INTO environment_cache_gateway_sessions(node_id,session_id,started_at,gateway_target_url)
SELECT n.id,sqlc.arg(session_id)::uuid,sqlc.arg(started_at)::timestamptz,n.gateway_target_url FROM compute_nodes n
WHERE n.name=sqlc.arg(node_name)::text AND n.active AND n.role IN ('compute-only','compute-node') AND coalesce(btrim(n.gateway_target_url),'')<>''
ON CONFLICT(node_id) DO UPDATE SET session_id=excluded.session_id,started_at=excluded.started_at,gateway_target_url=excluded.gateway_target_url,heartbeat_at=clock_timestamp()
WHERE (environment_cache_gateway_sessions.session_id=excluded.session_id
 AND environment_cache_gateway_sessions.started_at=excluded.started_at
 AND environment_cache_gateway_sessions.gateway_target_url=excluded.gateway_target_url)
 OR (environment_cache_gateway_sessions.session_id<>excluded.session_id AND environment_cache_gateway_sessions.started_at<excluded.started_at);

-- name: RefreshEnvironmentCacheGatewaySession :execrows
UPDATE environment_cache_gateway_sessions s SET heartbeat_at=clock_timestamp()
FROM compute_nodes n WHERE n.id=s.node_id AND n.name=sqlc.arg(node_name)::text AND s.session_id=sqlc.arg(session_id)::uuid
AND n.active AND n.role IN ('compute-only','compute-node') AND n.gateway_target_url=s.gateway_target_url;

-- name: RecordEnvironmentCacheGatewayObserved :execrows
INSERT INTO environment_cache_gateway_observations(node_id,app_id,environment_id,session_id,revision)
SELECT n.id,p.app_id,p.environment_id,s.session_id,p.revision
FROM project_environment_cache_policies p JOIN environment_cache_gateway_sessions s ON s.session_id=sqlc.arg(session_id)::uuid
JOIN compute_nodes n ON n.id=s.node_id
JOIN apps a ON a.id=p.app_id AND a.account_id=p.account_id AND a.project_id=p.project_id AND a.status<>'deleted'
JOIN project_environments e ON e.id=p.environment_id AND e.project_id=a.project_id AND e.account_id=a.account_id
WHERE n.name=sqlc.arg(node_name)::text AND n.active AND n.role IN ('compute-only','compute-node') AND n.gateway_target_url=s.gateway_target_url
AND s.heartbeat_at BETWEEN clock_timestamp()-interval '90 seconds' AND clock_timestamp()
AND p.app_id=sqlc.arg(app_id)::uuid AND p.environment_id=sqlc.arg(environment_id)::uuid AND p.revision=sqlc.arg(revision)::bigint
ON CONFLICT(node_id,app_id,environment_id) DO UPDATE SET session_id=excluded.session_id,revision=excluded.revision,observed_at=clock_timestamp();

-- name: ListEnvironmentCacheGatewayPolicies :many
SELECT p.account_id::text AS account_id,p.app_id::text AS app_id,p.environment_id::text AS environment_id,e.slug AS scope,p.revision
FROM project_environment_cache_policies p JOIN apps a ON a.id=p.app_id AND a.account_id=p.account_id AND a.project_id=p.project_id AND a.status<>'deleted'
JOIN project_environments e ON e.id=p.environment_id AND e.project_id=a.project_id AND e.account_id=a.account_id
JOIN compute_nodes n ON n.name=sqlc.arg(node_name)::text
JOIN environment_cache_gateway_sessions s ON s.node_id=n.id AND s.session_id=sqlc.arg(session_id)::uuid
WHERE (p.app_id::text||'/'||p.environment_id::text)>sqlc.arg(after_key)::text
AND NOT EXISTS (SELECT 1 FROM environment_cache_gateway_observations o WHERE o.node_id=n.id AND o.session_id=s.session_id AND o.app_id=p.app_id AND o.environment_id=p.environment_id AND o.revision=p.revision)
ORDER BY p.app_id::text||'/'||p.environment_id::text LIMIT 100;

-- name: ReadEnvironmentCacheConvergenceCounts :one
WITH sampled AS (SELECT clock_timestamp()::timestamptz AS checked_at), fleet AS (
 SELECT n.id, s.session_id,
 (n.last_heartbeat_at IS NULL OR n.last_heartbeat_at<sampled.checked_at-interval '90 seconds' OR n.last_heartbeat_at>sampled.checked_at
 OR s.session_id IS NULL OR s.gateway_target_url IS DISTINCT FROM n.gateway_target_url
 OR s.heartbeat_at<sampled.checked_at-interval '90 seconds' OR s.heartbeat_at>sampled.checked_at
 OR coalesce((o.session_id=s.session_id AND (o.observed_at>sampled.checked_at)),false)) AS stale,
 (o.session_id=s.session_id AND o.revision=sqlc.arg(revision)::bigint AND o.observed_at<=sampled.checked_at) AS acknowledged
 FROM compute_nodes n CROSS JOIN sampled
 LEFT JOIN environment_cache_gateway_sessions s ON s.node_id=n.id
 LEFT JOIN environment_cache_gateway_observations o ON o.node_id=n.id AND o.app_id=sqlc.arg(app_id)::uuid AND o.environment_id=sqlc.arg(environment_id)::uuid
 WHERE n.active AND n.role IN ('compute-only','compute-node') AND coalesce(btrim(n.gateway_target_url),'')<>''
)
SELECT sampled.checked_at::timestamptz AS checked_at,
 (SELECT count(*) FROM fleet)::bigint AS serving,
 (SELECT count(*) FROM fleet WHERE NOT stale AND acknowledged)::bigint AS acknowledged,
 (SELECT count(*) FROM fleet WHERE NOT stale AND NOT coalesce(acknowledged,false))::bigint AS pending,
 (SELECT count(*) FROM fleet WHERE stale)::bigint AS stale
FROM sampled;

-- name: WriteEnvironmentCacheHistory :exec
INSERT INTO environment_cache_policy_history(account_id,app_id,project_id,environment_id,revision,snapshot)
VALUES(sqlc.arg(account_id)::uuid,sqlc.arg(app_id)::uuid,sqlc.arg(project_id)::uuid,sqlc.arg(environment_id)::uuid,sqlc.arg(revision)::bigint,sqlc.arg(snapshot)::jsonb);

-- name: ReadEnvironmentCacheHistory :one
SELECT revision,snapshot FROM environment_cache_policy_history WHERE account_id=sqlc.arg(account_id)::uuid AND app_id=sqlc.arg(app_id)::uuid AND environment_id=sqlc.arg(environment_id)::uuid AND revision=sqlc.arg(revision)::bigint;

-- name: ListEnvironmentCacheHistory :many
SELECT revision,snapshot FROM environment_cache_policy_history WHERE account_id=sqlc.arg(account_id)::uuid AND app_id=sqlc.arg(app_id)::uuid AND environment_id=sqlc.arg(environment_id)::uuid AND (sqlc.arg(before_revision)::bigint=0 OR revision<sqlc.arg(before_revision)::bigint)
ORDER BY revision DESC LIMIT sqlc.arg(page_limit)::int;

-- name: LockEnvironmentCachePurgeSequence :exec
SELECT pg_advisory_xact_lock(711901248674);

-- name: InsertEnvironmentCachePurge :one
INSERT INTO environment_cache_purges(account_id,app_id,project_id,environment_id,request_id,path)
VALUES(sqlc.arg(account_id)::uuid,sqlc.arg(app_id)::uuid,sqlc.arg(project_id)::uuid,sqlc.arg(environment_id)::uuid,sqlc.arg(request_id)::uuid,sqlc.arg(path)::text)
RETURNING to_jsonb(environment_cache_purges) AS receipt;

-- name: ReadEnvironmentCachePurge :one
SELECT to_jsonb(p) AS receipt FROM environment_cache_purges p WHERE account_id=sqlc.arg(account_id)::uuid AND request_id=sqlc.arg(request_id)::uuid;

-- name: LatestEnvironmentCachePurge :one
SELECT coalesce(max(id),0)::bigint AS id FROM environment_cache_purges;

-- name: ListEnvironmentCachePurgesAfter :many
SELECT p.id,p.app_id::text AS app_id,p.environment_id::text AS environment_id,p.path FROM environment_cache_purges p
WHERE p.id>sqlc.arg(after_id)::bigint ORDER BY p.id LIMIT 100;

-- name: RecordEnvironmentCachePurgeWatermark :execrows
INSERT INTO environment_cache_purge_watermarks(node_id,session_id,last_id)
SELECT n.id,s.session_id,sqlc.arg(last_id)::bigint FROM compute_nodes n JOIN environment_cache_gateway_sessions s ON s.node_id=n.id
WHERE n.name=sqlc.arg(node_name)::text AND s.session_id=sqlc.arg(session_id)::uuid AND s.gateway_target_url=n.gateway_target_url
AND n.active AND n.role IN ('compute-only','compute-node') AND s.heartbeat_at BETWEEN clock_timestamp()-interval '90 seconds' AND clock_timestamp()
ON CONFLICT(node_id) DO UPDATE SET session_id=excluded.session_id,last_id=excluded.last_id,observed_at=clock_timestamp()
WHERE environment_cache_purge_watermarks.session_id<>excluded.session_id OR environment_cache_purge_watermarks.last_id<=excluded.last_id;

-- name: ReadEnvironmentCachePurgeCounts :one
WITH sampled AS (SELECT clock_timestamp()::timestamptz AS checked_at), fleet AS (
 SELECT n.id,
 (n.last_heartbeat_at IS NULL OR n.last_heartbeat_at<sampled.checked_at-interval '90 seconds' OR n.last_heartbeat_at>sampled.checked_at
 OR s.session_id IS NULL OR s.gateway_target_url IS DISTINCT FROM n.gateway_target_url
 OR s.heartbeat_at<sampled.checked_at-interval '90 seconds' OR s.heartbeat_at>sampled.checked_at
 OR coalesce(w.session_id=s.session_id AND w.observed_at>sampled.checked_at,false)) AS stale,
 (w.session_id=s.session_id AND w.last_id>=sqlc.arg(generation)::bigint AND w.observed_at<=sampled.checked_at) AS acknowledged
 FROM compute_nodes n CROSS JOIN sampled
 LEFT JOIN environment_cache_gateway_sessions s ON s.node_id=n.id
 LEFT JOIN environment_cache_purge_watermarks w ON w.node_id=n.id
 WHERE n.active AND n.role IN ('compute-only','compute-node') AND coalesce(btrim(n.gateway_target_url),'')<>''
)
SELECT sampled.checked_at::timestamptz AS checked_at,
 (SELECT count(*) FROM fleet)::bigint AS serving,
 (SELECT count(*) FROM fleet WHERE NOT stale AND acknowledged)::bigint AS acknowledged,
 (SELECT count(*) FROM fleet WHERE NOT stale AND NOT coalesce(acknowledged,false))::bigint AS pending,
 (SELECT count(*) FROM fleet WHERE stale)::bigint AS stale
FROM sampled;

-- name: InsertEnvironmentCacheSchedule :one
INSERT INTO environment_cache_schedules(schedule_id,account_id,app_id,project_id,environment_id,scope,request,execute_at)
VALUES(sqlc.arg(schedule_id)::uuid,sqlc.arg(account_id)::uuid,sqlc.arg(app_id)::uuid,sqlc.arg(project_id)::uuid,sqlc.arg(environment_id)::uuid,sqlc.arg(scope)::text,sqlc.arg(request)::jsonb,sqlc.arg(execute_at)::timestamptz)
RETURNING to_jsonb(request || (to_jsonb(environment_cache_schedules)-'request'-'result') || result) AS snapshot;

-- name: ReadEnvironmentCacheSchedule :one
SELECT to_jsonb(request || (to_jsonb(s)-'request'-'result') || result) AS snapshot FROM environment_cache_schedules s
WHERE schedule_id=sqlc.arg(schedule_id)::uuid AND account_id=sqlc.arg(account_id)::uuid AND app_id=sqlc.arg(app_id)::uuid AND environment_id=sqlc.arg(environment_id)::uuid;

-- name: CountEnvironmentCacheSchedules :one
SELECT count(*)::bigint FROM environment_cache_schedules WHERE account_id=sqlc.arg(account_id)::uuid AND app_id=sqlc.arg(app_id)::uuid AND environment_id=sqlc.arg(environment_id)::uuid AND status IN ('scheduled','executing');

-- name: CancelEnvironmentCacheSchedule :execrows
UPDATE environment_cache_schedules SET status='cancelled',finished_at=clock_timestamp()
WHERE schedule_id=sqlc.arg(schedule_id)::uuid AND account_id=sqlc.arg(account_id)::uuid AND app_id=sqlc.arg(app_id)::uuid AND environment_id=sqlc.arg(environment_id)::uuid AND status='scheduled';

-- name: ClaimEnvironmentCacheSchedule :one
WITH candidate AS (
 SELECT schedule_id FROM environment_cache_schedules
 WHERE execute_at<=clock_timestamp() AND (status='scheduled' OR (status='executing' AND lease_until<=clock_timestamp()))
 ORDER BY execute_at,schedule_id FOR UPDATE SKIP LOCKED LIMIT 1
)
UPDATE environment_cache_schedules s SET status='executing',lease_token=sqlc.arg(lease_token)::uuid,lease_until=clock_timestamp()+interval '60 seconds'
FROM candidate c WHERE s.schedule_id=c.schedule_id
RETURNING s.account_id::text AS account_id,to_jsonb(s.request || (to_jsonb(s)-'request'-'result') || s.result) AS snapshot;

-- name: FinishEnvironmentCacheSchedule :execrows
UPDATE environment_cache_schedules SET status=sqlc.arg(status)::text,result=sqlc.arg(result)::jsonb,error_code=sqlc.arg(error_code)::text,finished_at=clock_timestamp(),lease_token=NULL,lease_until=NULL
WHERE schedule_id=sqlc.arg(schedule_id)::uuid AND status='executing' AND lease_token=sqlc.arg(lease_token)::uuid;

-- name: LockEnvironmentCacheSchedule :one
SELECT to_jsonb(request || (to_jsonb(s)-'request'-'result') || result) AS snapshot FROM environment_cache_schedules s
WHERE schedule_id=sqlc.arg(schedule_id)::uuid AND account_id=sqlc.arg(account_id)::uuid AND app_id=sqlc.arg(app_id)::uuid AND environment_id=sqlc.arg(environment_id)::uuid FOR UPDATE;

-- name: ReplaceEnvironmentCacheSchedule :execrows
UPDATE environment_cache_schedules SET status='cancelled',finished_at=clock_timestamp(),replaced_by_schedule_id=sqlc.arg(replacement_id)::uuid
WHERE schedule_id=sqlc.arg(schedule_id)::uuid AND status='scheduled';

-- name: LinkEnvironmentCacheScheduleReplacement :exec
UPDATE environment_cache_schedules SET replaces_schedule_id=sqlc.arg(original_id)::uuid WHERE schedule_id=sqlc.arg(schedule_id)::uuid;

-- name: ListEnvironmentCacheSchedules :many
SELECT to_jsonb(request || (to_jsonb(s)-'request'-'result') || result) AS snapshot FROM environment_cache_schedules s
WHERE account_id=sqlc.arg(account_id)::uuid AND app_id=sqlc.arg(app_id)::uuid AND environment_id=sqlc.arg(environment_id)::uuid
AND (sqlc.arg(status_filter)::text='' OR status=sqlc.arg(status_filter)::text)
AND (sqlc.narg(from_time)::timestamptz IS NULL OR execute_at>=sqlc.narg(from_time)::timestamptz)
AND (sqlc.narg(until_time)::timestamptz IS NULL OR execute_at<sqlc.narg(until_time)::timestamptz)
AND (NOT sqlc.arg(has_cursor)::boolean OR (execute_at,schedule_id)>(sqlc.arg(after_time)::timestamptz,sqlc.arg(after_id)::uuid))
ORDER BY execute_at,schedule_id LIMIT sqlc.arg(page_limit)::int;

-- name: AppendEnvironmentCacheScheduleEvent :exec
WITH allocated AS (
 INSERT INTO environment_cache_schedule_event_counters(account_id,app_id,project_id,environment_id,last_sequence)
 VALUES(sqlc.arg(account_id)::uuid,sqlc.arg(app_id)::uuid,sqlc.arg(project_id)::uuid,sqlc.arg(environment_id)::uuid,1)
 ON CONFLICT(app_id,environment_id) DO UPDATE SET last_sequence=environment_cache_schedule_event_counters.last_sequence+1
 RETURNING last_sequence
)
INSERT INTO environment_cache_schedule_events(account_id,app_id,project_id,environment_id,scope,sequence,payload)
SELECT sqlc.arg(account_id)::uuid,sqlc.arg(app_id)::uuid,sqlc.arg(project_id)::uuid,sqlc.arg(environment_id)::uuid,sqlc.arg(scope)::text,last_sequence,sqlc.arg(payload)::jsonb FROM allocated;

-- name: ReadEnvironmentCacheScheduleEventHead :one
SELECT coalesce((SELECT last_sequence FROM environment_cache_schedule_event_counters WHERE account_id=sqlc.arg(account_id)::uuid AND app_id=sqlc.arg(app_id)::uuid AND environment_id=sqlc.arg(environment_id)::uuid),0)::bigint AS sequence;

-- name: ReadEnvironmentCacheScheduleEvents :many
SELECT scope,to_jsonb(payload || jsonb_build_object('sequence',sequence,'occurred_at',occurred_at)) AS event
FROM environment_cache_schedule_events WHERE account_id=sqlc.arg(account_id)::uuid AND app_id=sqlc.arg(app_id)::uuid AND environment_id=sqlc.arg(environment_id)::uuid AND sequence>sqlc.arg(after_sequence)::bigint
ORDER BY sequence LIMIT sqlc.arg(page_limit)::int;

-- name: LockCacheScheduleCompletionAccount :one
SELECT id FROM accounts WHERE id=sqlc.arg(account_id)::uuid FOR UPDATE;

-- name: LockCacheScheduleCompletionApp :one
SELECT id FROM apps WHERE id=sqlc.arg(app_id)::uuid AND account_id=sqlc.arg(account_id)::uuid FOR UPDATE;

-- name: LockCacheScheduleCompletionEnvironment :one
SELECT id FROM project_environments WHERE id=sqlc.arg(environment_id)::uuid AND project_id=sqlc.arg(project_id)::uuid AND account_id=sqlc.arg(account_id)::uuid FOR UPDATE;

-- name: ReadEnvironmentCacheScheduleEventBounds :one
SELECT coalesce((SELECT last_sequence FROM environment_cache_schedule_event_counters WHERE account_id=sqlc.arg(account_id)::uuid AND app_id=sqlc.arg(app_id)::uuid AND environment_id=sqlc.arg(environment_id)::uuid),0)::bigint AS head,
coalesce((SELECT pruned_through FROM environment_cache_schedule_event_counters WHERE account_id=sqlc.arg(account_id)::uuid AND app_id=sqlc.arg(app_id)::uuid AND environment_id=sqlc.arg(environment_id)::uuid),0)::bigint AS floor;

-- name: LockEnvironmentCacheScheduleRetentionTargets :many
SELECT c.app_id,c.environment_id,c.pruned_through
FROM environment_cache_schedule_event_counters c
JOIN environment_cache_schedule_events e ON e.app_id=c.app_id AND e.environment_id=c.environment_id AND e.sequence=c.pruned_through+1
WHERE e.occurred_at<sqlc.arg(cutoff)::timestamptz
ORDER BY e.occurred_at,c.app_id,c.environment_id
LIMIT sqlc.arg(target_limit)::int FOR UPDATE OF c SKIP LOCKED;

-- name: PruneEnvironmentCacheScheduleEvents :exec
WITH prefix AS (
 SELECT sequence,occurred_at FROM environment_cache_schedule_events
 WHERE app_id=sqlc.arg(app_id)::uuid AND environment_id=sqlc.arg(environment_id)::uuid
 AND sequence>sqlc.arg(pruned_through)::bigint
 ORDER BY sequence LIMIT sqlc.arg(batch_limit)::int
), boundary AS (
 SELECT coalesce(min(sequence) FILTER (WHERE occurred_at>=sqlc.arg(cutoff)::timestamptz)-1,max(sequence)) AS through FROM prefix
), removed AS (
 DELETE FROM environment_cache_schedule_events
 WHERE app_id=sqlc.arg(app_id)::uuid AND environment_id=sqlc.arg(environment_id)::uuid
 AND sequence<= (SELECT through FROM boundary) RETURNING sequence
)
UPDATE environment_cache_schedule_event_counters
SET pruned_through=coalesce((SELECT max(sequence) FROM removed),pruned_through)
WHERE app_id=sqlc.arg(app_id)::uuid AND environment_id=sqlc.arg(environment_id)::uuid;
