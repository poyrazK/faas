-- name: OperatorOverviewTotals :one
WITH accounts_summary AS (
    SELECT count(*) FILTER (WHERE status = 'active')::bigint AS accounts_active,
           count(*) FILTER (WHERE status = 'past_due')::bigint AS accounts_past_due,
           count(*) FILTER (WHERE status = 'suspended')::bigint AS accounts_suspended
    FROM accounts
), instances_summary AS (
    SELECT count(*) FILTER (WHERE state = 'running')::bigint AS instances_live,
           count(*) FILTER (WHERE state IN ('waking', 'cold_booting'))::bigint AS instances_waking
    FROM instances
), nodes_summary AS (
    SELECT count(*) FILTER (WHERE active)::bigint AS nodes_active,
           count(*) FILTER (WHERE NOT active)::bigint AS nodes_inactive
    FROM compute_nodes
)
SELECT a.accounts_active, a.accounts_past_due, a.accounts_suspended,
       i.instances_live, i.instances_waking, n.nodes_active, n.nodes_inactive,
       (SELECT count(*) FROM apps WHERE status <> 'deleted')::bigint AS apps_total,
       (SELECT count(*) FROM orgs)::bigint AS orgs_total,
       (SELECT count(*) FROM audit_log
        WHERE received_at >= sqlc.arg(audit_since)::timestamptz
          AND received_at <= sqlc.arg(observed_at)::timestamptz)::bigint AS audit_events_24h
FROM accounts_summary a CROSS JOIN instances_summary i CROSS JOIN nodes_summary n;

-- name: OperatorOverviewFunnel :one
-- last_request_at retains successful-request evidence, including terminal
-- instances and deleted apps. It is not an immutable first-ever timestamp.
WITH cohort AS MATERIALIZED (
    SELECT id, created_at,
           email_verified_at IS NOT NULL AND email_verified_at <= sqlc.arg(observed_at)::timestamptz AS verified
    FROM accounts
    WHERE created_at >= sqlc.arg(cohort_since)::timestamptz
      AND created_at <= sqlc.arg(observed_at)::timestamptz
), workloads AS (
    SELECT a.account_id, bool_or(d.id IS NOT NULL) AS with_live
    FROM cohort c JOIN apps a ON a.account_id = c.id AND a.status <> 'deleted'
    LEFT JOIN deployments d ON d.app_id = a.id AND d.status = 'live' AND d.deleted_at IS NULL
    WHERE c.verified
    GROUP BY a.account_id
), successes AS (
    SELECT a.account_id, min(i.last_request_at) AS first_success
    FROM cohort c JOIN workloads w ON w.account_id = c.id AND w.with_live
    JOIN apps a ON a.account_id = c.id
    JOIN instances i ON i.app_id = a.id
    WHERE i.last_request_at >= c.created_at AND i.last_request_at <= sqlc.arg(observed_at)::timestamptz
    GROUP BY a.account_id
), samples AS (
    SELECT c.verified, w.account_id IS NOT NULL AS with_app,
           coalesce(w.with_live, false) AS with_live,
           trunc(extract(epoch FROM s.first_success - c.created_at))::bigint AS latency_seconds
    FROM cohort c LEFT JOIN workloads w ON w.account_id = c.id
    LEFT JOIN successes s ON s.account_id = c.id
)
SELECT count(*)::bigint AS accounts_created,
       count(*) FILTER (WHERE verified)::bigint AS email_verified,
       count(*) FILTER (WHERE with_app)::bigint AS with_app,
       count(*) FILTER (WHERE with_live)::bigint AS with_live_deployment,
       count(latency_seconds)::bigint AS with_successful_request,
       coalesce(percentile_disc(0.50) WITHIN GROUP (ORDER BY latency_seconds), 0)::bigint AS p50_seconds,
       coalesce(percentile_disc(0.95) WITHIN GROUP (ORDER BY latency_seconds), 0)::bigint AS p95_seconds
FROM samples;

-- name: OperatorOverviewNodes :many
SELECT name, active, last_heartbeat_at FROM compute_nodes ORDER BY name, id;

-- name: OperatorOverviewFailures :many
WITH counts AS (
    SELECT kind, count(*)::bigint AS hits FROM events
    WHERE at >= sqlc.arg(since)::timestamptz AND at <= sqlc.arg(observed_at)::timestamptz
      AND (kind LIKE '%.failed' OR kind LIKE '%.failure' OR kind LIKE '%.error')
    GROUP BY kind
), ranked AS (
    SELECT kind, hits, row_number() OVER (ORDER BY hits DESC, kind) AS position FROM counts
), buckets AS (
    SELECT kind, hits, position FROM ranked WHERE position <= sqlc.arg(top_n)::integer
    UNION ALL
    SELECT 'other'::text, sum(hits)::bigint, sqlc.arg(top_n)::bigint + 1
    FROM ranked WHERE position > sqlc.arg(top_n)::integer HAVING count(*) > 0
)
SELECT kind, hits FROM buckets ORDER BY position;

-- name: OperatorOverviewRateLimited :many
SELECT coalesce(subject, '00000000-0000-0000-0000-000000000000'::uuid)::text AS account_id,
       count(*)::bigint AS hits
FROM events
WHERE kind = 'auth.rate_limited' AND at >= sqlc.arg(since)::timestamptz
  AND at <= sqlc.arg(observed_at)::timestamptz
GROUP BY coalesce(subject, '00000000-0000-0000-0000-000000000000'::uuid)
ORDER BY hits DESC, max(at) DESC, account_id
LIMIT sqlc.arg(top_n)::integer;

-- name: OperatorOverviewActivity :one
SELECT (SELECT count(*) FROM orgs)::bigint AS orgs_total,
       (SELECT count(*) FROM audit_log WHERE received_at >= sqlc.arg(audit_since))::bigint AS audit_events_24h;

-- name: OperatorRecentFailureCounts :many
SELECT kind, count(*)::bigint AS hits
FROM events
WHERE at >= sqlc.arg(since) AND (kind LIKE '%.failed' OR kind LIKE '%.failure' OR kind LIKE '%.error')
GROUP BY kind ORDER BY hits DESC, kind;

-- name: OperatorAuditTraceCoverage :one
SELECT coalesce(count(*) FILTER (WHERE data->>'trace_id' ~ '^[0-9a-f]{32}$')::double precision / nullif(count(*), 0), 1.0)::double precision AS ratio
FROM audit_log WHERE received_at >= sqlc.arg(since);

-- name: OperatorInsertAuditLog :exec
INSERT INTO audit_log (id, kind, account_id, account_email, actor, received_at, data)
VALUES (sqlc.arg(id)::uuid, sqlc.arg(kind)::text, sqlc.narg(account_id)::uuid,
        sqlc.narg(account_email)::text, sqlc.narg(actor)::text,
        sqlc.arg(received_at)::timestamptz, sqlc.narg(data)::jsonb);

-- name: OperatorTenantCounts :one
SELECT (SELECT count(*) FROM apps owned WHERE owned.account_id = sqlc.arg(target_account_id) AND owned.status <> 'deleted')::bigint AS apps_count,
       (SELECT count(*) FROM deployments d JOIN apps a ON a.id = d.app_id
        WHERE a.account_id = sqlc.arg(target_account_id) AND a.status <> 'deleted'
          AND d.status = 'live' AND d.deleted_at IS NULL)::bigint AS deployments_live_count;

-- name: OperatorTenantApps :many
SELECT a.id::text AS id, a.slug, a.status,
       count(d.id)::bigint AS deployments_live
FROM apps a
LEFT JOIN deployments d ON d.app_id = a.id AND d.status = 'live' AND d.deleted_at IS NULL
WHERE a.account_id = sqlc.arg(target_account_id)::uuid AND a.status <> 'deleted'
GROUP BY a.id, a.slug, a.status, a.created_at
ORDER BY a.created_at DESC, a.id DESC;

-- name: OperatorCapacityProfiles :many
-- The API catalog is the single source of named shapes. Unmatched shapes
-- share one custom bucket, keeping the result bounded as the fleet grows.
-- Aggregate instances before joining so app reservations are counted once.
WITH catalog AS (
    SELECT p.value->>'name' AS name,
           (p.value->>'memory_mb')::integer AS memory_mb,
           (p.value->>'cpu_millicores')::integer AS cpu_millicores,
           (p.value->>'sort_order')::integer AS sort_order
    FROM jsonb_array_elements(sqlc.arg(resource_profiles)::jsonb) AS p(value)
), live AS (
    SELECT app_id, count(*)::bigint AS instances_live
    FROM instances WHERE state IN ('running', 'waking', 'cold_booting')
    GROUP BY app_id
)
SELECT coalesce(c.name, 'custom')::text AS resource_profile,
       coalesce(c.memory_mb, 0)::integer AS memory_mb,
       coalesce(c.cpu_millicores, 0)::integer AS cpu_millicores,
       count(*)::bigint AS apps,
       coalesce(sum(l.instances_live), 0)::bigint AS live_instances,
       sum(a.ram_mb)::bigint AS reserved_memory_mb,
       sum(a.cpu_millicores)::bigint AS reserved_cpu_millicores
FROM apps a
LEFT JOIN catalog c ON c.memory_mb = a.ram_mb AND c.cpu_millicores = a.cpu_millicores
LEFT JOIN live l ON l.app_id = a.id
WHERE a.status <> 'deleted'
GROUP BY c.name, c.memory_mb, c.cpu_millicores, c.sort_order
ORDER BY c.sort_order NULLS LAST, resource_profile;

-- name: OperatorIncidents :many
WITH signals AS (
    SELECT ('deployment/' || d.id::text)::text AS id, 'deployment'::text AS type,
           CASE WHEN d.status = 'failed' THEN 'error' ELSE 'warning' END::text AS severity,
           d.status::text AS status,
           ('deployment ' || d.status || CASE WHEN d.status = 'failed' AND coalesce(d.error_code, '') <> ''
             THEN ' (' || d.error_code || ')' ELSE '' END)::text AS summary,
           a.account_id::text AS account_id, a.id::text AS app_id,
           d.id::text AS resource_id, a.slug::text AS resource_name, d.created_at AS observed_at
    FROM deployments d JOIN apps a ON a.id = d.app_id
    WHERE a.status <> 'deleted' AND d.deleted_at IS NULL
      AND d.status IN ('pending', 'building', 'imaging', 'snapshotting', 'failed')
      AND (d.status <> 'failed' OR d.created_at >= sqlc.arg(since))
    UNION ALL
    SELECT 'job_run/' || r.id::text, 'job_run'::text,
           CASE WHEN r.aggregate_status IN ('failed', 'dead_letter') OR r.created_at <= sqlc.arg(stuck_before)
             THEN 'error' ELSE 'warning' END::text,
           r.aggregate_status::text,
           CASE WHEN r.aggregate_status IN ('failed', 'dead_letter') THEN 'job run ' || r.aggregate_status
             WHEN r.created_at <= sqlc.arg(stuck_before) THEN 'job run appears stuck'
             ELSE 'job run ' || r.aggregate_status END::text,
           r.account_id::text, ''::text, r.id::text, j.name::text,
           coalesce(r.finished_at, r.created_at)
    FROM job_runs r JOIN jobs j ON j.id = r.job_id AND j.account_id = r.account_id
    WHERE r.aggregate_status IN ('queued', 'running', 'failed', 'dead_letter')
      AND (r.aggregate_status IN ('queued', 'running') OR coalesce(r.finished_at, r.created_at) >= sqlc.arg(since))
    UNION ALL
    SELECT 'compute_node/' || n.id::text, 'compute_node'::text,
           CASE WHEN n.active THEN 'warning' ELSE 'error' END::text,
           CASE WHEN n.last_heartbeat_at < sqlc.arg(stale_before) THEN 'stale' ELSE n.lifecycle::text END::text,
           CASE WHEN n.last_heartbeat_at < sqlc.arg(stale_before) THEN 'compute node heartbeat stale'
             ELSE 'compute node ' || n.lifecycle::text END::text,
           ''::text, ''::text, n.id::text, n.name::text,
           coalesce(n.drain_initiated_at, n.last_heartbeat_at, n.created_at)
    FROM compute_nodes n
    WHERE NOT n.active OR n.lifecycle <> 'active' OR n.last_heartbeat_at < sqlc.arg(stale_before)
)
SELECT id, type, severity, status, summary, account_id, app_id, resource_id, resource_name, observed_at
FROM signals
WHERE (sqlc.arg(kind)::text = '' OR type = sqlc.arg(kind))
  AND (sqlc.arg(severity)::text = '' OR severity = sqlc.arg(severity))
  AND (sqlc.arg(before_id)::text = '' OR (observed_at, id) < (sqlc.arg(before_at)::timestamptz, sqlc.arg(before_id)::text))
ORDER BY observed_at DESC, id DESC
LIMIT sqlc.arg(page_limit);

-- name: OperatorIncidentEvidence :many
-- Each branch uses an exact-resource index and is independently bounded before
-- the merge. Payload references use the partial operator-resource index.
WITH history AS (
    (SELECT e.id::text AS id, 'event'::text AS source, e.kind::text AS kind,
            ''::text AS status, e.at AS at, coalesce(e.trace_id, '')::text AS trace_id
     FROM events e WHERE e.subject = sqlc.arg(target_id)::uuid AND e.at >= sqlc.arg(since)
     ORDER BY e.at DESC, e.id::text COLLATE "C" DESC LIMIT sqlc.arg(page_limit))
    UNION ALL
    (SELECT e.id::text, 'event'::text, e.kind::text, ''::text, e.at, coalesce(e.trace_id, '')::text
     FROM events e
     WHERE e.kind LIKE 'operator.action.%'
       AND coalesce(nullif(e.data->>'target_id', ''), nullif(e.data->>'run_id', ''), nullif(e.data->>'deployment_id', '')) = sqlc.arg(target_id)::text
       AND e.subject IS DISTINCT FROM sqlc.arg(target_id)::uuid AND e.at >= sqlc.arg(since)
     ORDER BY e.at DESC, e.id::text COLLATE "C" DESC LIMIT sqlc.arg(page_limit))
    UNION ALL
    (SELECT a.id::text, 'deployment_audit'::text, a.kind::text, ''::text, a.at, ''::text
     FROM deployment_audit a WHERE a.deployment_id = sqlc.arg(target_id)::uuid AND a.at >= sqlc.arg(since)
     ORDER BY a.at DESC, a.id::text COLLATE "C" DESC LIMIT sqlc.arg(page_limit))
    UNION ALL
    (SELECT i.id::text, 'operator_intent'::text, i.kind::text, i.status::text,
            i.requested_at, coalesce(i.trace_id, '')::text
     FROM operator_intents i WHERE i.target_id = sqlc.arg(target_id)::text AND i.requested_at >= sqlc.arg(since)
     ORDER BY i.requested_at DESC, i.id::text COLLATE "C" DESC LIMIT sqlc.arg(page_limit))
)
SELECT id, source, kind, status, at, trace_id FROM history
ORDER BY at DESC, source COLLATE "C" DESC, id COLLATE "C" DESC LIMIT sqlc.arg(page_limit);

-- name: OperatorAlertCollectionLock :one
SELECT last_attempt_at, last_success_at FROM operator_alert_collection WHERE singleton FOR UPDATE;

-- name: OperatorAlertCollection :one
SELECT last_attempt_at, last_success_at FROM operator_alert_collection WHERE singleton;

-- name: OperatorAlertCollectionSucceeded :exec
UPDATE operator_alert_collection SET last_attempt_at = sqlc.arg(at), last_success_at = sqlc.arg(at) WHERE singleton;

-- name: OperatorAlertCollectionFailed :exec
UPDATE operator_alert_collection SET last_attempt_at = sqlc.arg(at)
WHERE singleton AND (last_attempt_at IS NULL OR last_attempt_at < sqlc.arg(at));

-- name: OperatorAlertAdvanceRules :many
INSERT INTO operator_alert_rule_observations (rule_key, evaluated_at, episode_ids)
SELECT r.key, r.evaluated_at, r.episode_ids FROM jsonb_to_recordset(sqlc.arg(rules)::jsonb) AS r(key text, evaluated_at timestamptz, episode_ids jsonb)
WHERE true
ON CONFLICT (rule_key) DO UPDATE SET evaluated_at = excluded.evaluated_at, episode_ids = excluded.episode_ids
WHERE excluded.evaluated_at > operator_alert_rule_observations.evaluated_at
   OR (excluded.evaluated_at = operator_alert_rule_observations.evaluated_at AND excluded.episode_ids = operator_alert_rule_observations.episode_ids)
RETURNING rule_key;

-- name: OperatorAlertResolveMissing :exec
UPDATE operator_alert_episodes e
SET resolved_at = r.evaluated_at, last_evaluated_at = r.evaluated_at, last_checked_at = sqlc.arg(observed_at)::timestamptz
FROM jsonb_to_recordset(sqlc.arg(rules)::jsonb) AS r(key text, evaluated_at timestamptz, episode_ids jsonb)
WHERE e.rule_key = r.key AND e.resolved_at IS NULL AND e.last_evaluated_at <= r.evaluated_at
  AND NOT EXISTS (SELECT 1 FROM jsonb_array_elements_text(r.episode_ids) AS current_ids(id) WHERE current_ids.id = e.id);

-- name: OperatorAlertUpsertEpisodes :exec
INSERT INTO operator_alert_episodes (id, series_id, rule_key, name, component, severity, account_id, app_id, node_id,
    runbook_url, started_at, first_seen_at, last_seen_at, last_evaluated_at, last_checked_at)
SELECT e.id, e.series_id, e.rule_key, e.name, e.component, e.severity,
       nullif(e.account_id, '')::uuid, nullif(e.app_id, '')::uuid, nullif(e.node_id, '')::uuid,
       e.runbook_url, e.started_at, sqlc.arg(observed_at)::timestamptz, sqlc.arg(observed_at)::timestamptz,
       e.last_evaluated_at, sqlc.arg(observed_at)::timestamptz
FROM jsonb_to_recordset(sqlc.arg(episodes)::jsonb) AS e(id text, series_id text, rule_key text, name text,
    component text, severity text, account_id text, app_id text, node_id text, runbook_url text,
    started_at timestamptz, last_evaluated_at timestamptz)
WHERE true
ON CONFLICT (id) DO UPDATE SET last_seen_at = excluded.last_seen_at,
    last_evaluated_at = excluded.last_evaluated_at, last_checked_at = excluded.last_checked_at,
    runbook_url = excluded.runbook_url
WHERE operator_alert_episodes.resolved_at IS NULL AND excluded.last_evaluated_at >= operator_alert_episodes.last_evaluated_at;

-- name: OperatorAlertEpisodes :many
WITH projected AS (
    SELECT e.*, CASE WHEN e.resolved_at IS NOT NULL THEN 'resolved'
      WHEN c.last_success_at IS NOT NULL AND c.last_attempt_at = c.last_success_at
       AND c.last_success_at >= sqlc.arg(fresh_since)::timestamptz AND c.last_success_at <= sqlc.arg(future_before)::timestamptz
       AND e.last_checked_at = c.last_success_at AND e.last_evaluated_at >= sqlc.arg(fresh_since)::timestamptz
       AND e.last_evaluated_at <= sqlc.arg(future_before)::timestamptz THEN 'firing' ELSE 'unknown' END AS current_status
    FROM operator_alert_episodes e CROSS JOIN operator_alert_collection c
    WHERE (e.resolved_at IS NULL OR e.resolved_at >= sqlc.arg(since)::timestamptz)
      AND (sqlc.arg(severity)::text = '' OR e.severity = sqlc.arg(severity)::text)
      AND (sqlc.arg(acknowledgment)::text = '' OR triage_acknowledged = (sqlc.arg(acknowledgment)::text = 'acknowledged'))
      AND (sqlc.arg(ownership)::text = '' OR sqlc.arg(ownership)::text = 'owned' AND triage_owner_id IS NOT NULL
        OR sqlc.arg(ownership)::text = 'unowned' AND triage_owner_id IS NULL
        OR sqlc.arg(ownership)::text = 'mine' AND triage_owner_id::text = sqlc.arg(owner_id)::text)
      AND (sqlc.arg(snooze)::text = '' OR coalesce(triage_snoozed_until > sqlc.arg(now_at)::timestamptz,false) = (sqlc.arg(snooze)::text = 'active'))
      AND (sqlc.arg(before_id)::text = '' OR (coalesce(e.resolved_at,e.started_at), 'platform_alert/' || e.id) < (sqlc.arg(before_at)::timestamptz, sqlc.arg(before_id)::text))
)
SELECT id, series_id, rule_key, name, component, severity, coalesce(account_id::text,'')::text AS account_id,
       coalesce(app_id::text,'')::text AS app_id, coalesce(node_id::text,'')::text AS node_id, runbook_url,
       started_at, first_seen_at, last_seen_at, last_evaluated_at, last_checked_at, resolved_at, triage_revision, triage_acknowledged,
 coalesce(triage_owner_id::text,'')::text AS triage_owner_id, triage_snoozed_until, triage_updated_at
FROM projected WHERE sqlc.arg(status)::text = '' OR current_status = sqlc.arg(status)::text
ORDER BY coalesce(resolved_at,started_at) DESC, id DESC LIMIT sqlc.arg(page_limit)::integer;

-- name: OperatorAlertEpisode :one
SELECT id, series_id, rule_key, name, component, severity, coalesce(account_id::text,'')::text AS account_id,
       coalesce(app_id::text,'')::text AS app_id, coalesce(node_id::text,'')::text AS node_id, runbook_url,
       started_at, first_seen_at, last_seen_at, last_evaluated_at, last_checked_at, resolved_at, triage_revision, triage_acknowledged,
 coalesce(triage_owner_id::text,'')::text AS triage_owner_id, triage_snoozed_until, triage_updated_at
FROM operator_alert_episodes WHERE id = sqlc.arg(id)::text OR series_id = sqlc.arg(id)::text
ORDER BY (id = sqlc.arg(id)::text) DESC, started_at DESC, first_seen_at DESC, id DESC LIMIT 1;

-- name: OperatorAlertPruneEpisodes :execrows
DELETE FROM operator_alert_episodes WHERE id IN (
    SELECT id FROM operator_alert_episodes WHERE coalesce(resolved_at,last_seen_at) < sqlc.arg(before)::timestamptz
    ORDER BY coalesce(resolved_at,last_seen_at), id LIMIT sqlc.arg(batch_size)::integer
);

-- name: OperatorAlertPruneRules :exec
DELETE FROM operator_alert_rule_observations WHERE rule_key IN (
    SELECT r.rule_key FROM operator_alert_rule_observations r WHERE r.evaluated_at < sqlc.arg(before)::timestamptz
    AND NOT EXISTS (SELECT 1 FROM operator_alert_episodes e WHERE e.rule_key = r.rule_key)
    ORDER BY r.evaluated_at, r.rule_key LIMIT sqlc.arg(batch_size)::integer
);

-- name: OperatorAlertRetentionSucceeded :exec
UPDATE operator_alert_retention_health SET last_attempt_at = sqlc.arg(at), last_success_at = sqlc.arg(at),
last_pruned_episodes = sqlc.arg(pruned)::bigint
WHERE singleton AND (last_attempt_at IS NULL OR last_attempt_at < sqlc.arg(at));

-- name: OperatorAlertRetentionLock :one
SELECT singleton FROM operator_alert_retention_health WHERE singleton FOR UPDATE;

-- name: OperatorAlertRetentionFailed :exec
UPDATE operator_alert_retention_health SET last_attempt_at = sqlc.arg(at)
WHERE singleton AND (last_attempt_at IS NULL OR last_attempt_at < sqlc.arg(at));

-- name: OperatorAlertRetentionStatus :one
SELECT h.last_attempt_at, h.last_success_at, h.last_pruned_episodes,
  (SELECT count(*) FROM operator_alert_episodes WHERE coalesce(resolved_at,last_seen_at) < sqlc.arg(cutoff)::timestamptz)::bigint AS expired_episodes,
  (SELECT min(coalesce(resolved_at,last_seen_at)) FROM operator_alert_episodes WHERE coalesce(resolved_at,last_seen_at) < sqlc.arg(cutoff)::timestamptz)::timestamptz AS oldest_expired_at
FROM operator_alert_retention_health h WHERE singleton;

-- name: OperatorAlertTriageLock :one
SELECT triage_revision, triage_acknowledged, coalesce(triage_owner_id::text,'')::text AS triage_owner_id,
 triage_snoozed_until, triage_updated_at
FROM operator_alert_episodes WHERE id = sqlc.arg(id)::text FOR UPDATE;

-- name: OperatorAlertTriageUpdate :execrows
UPDATE operator_alert_episodes SET triage_revision = sqlc.arg(revision)::bigint,
 triage_acknowledged = sqlc.arg(acknowledged)::boolean, triage_owner_id = sqlc.narg(owner_id)::uuid,
 triage_snoozed_until = sqlc.narg(snoozed_until)::timestamptz, triage_updated_at = sqlc.arg(updated_at)::timestamptz
WHERE id = sqlc.arg(id)::text AND triage_revision = sqlc.arg(expected_revision)::bigint;

-- name: OperatorAlertTriageHistoryCoverage :one
WITH receipts AS (
 SELECT CASE WHEN jsonb_typeof(data#>'{triage,revision}') = 'number' AND (data#>>'{triage,revision}') ~ '^[1-9][0-9]{0,15}$' THEN (data#>>'{triage,revision}')::bigint END AS revision
 FROM audit_log WHERE kind = 'operator.action.alert_triage' AND data->>'episode_id' = sqlc.arg(episode_id)::text
)
SELECT count(*)::bigint AS total_entries, count(DISTINCT revision)::bigint AS distinct_entries,
 coalesce(min(revision),0)::bigint AS oldest_revision, coalesce(max(revision),0)::bigint AS newest_revision
FROM receipts WHERE revision <= sqlc.arg(snapshot_revision)::bigint OR revision IS NULL;

-- name: OperatorAlertTriageHistoryEntries :many
WITH receipts AS (
 SELECT id, received_at, data, CASE WHEN jsonb_typeof(data#>'{triage,revision}') = 'number' AND (data#>>'{triage,revision}') ~ '^[1-9][0-9]{0,15}$' THEN (data#>>'{triage,revision}')::bigint END AS revision
 FROM audit_log WHERE kind = 'operator.action.alert_triage' AND data->>'episode_id' = sqlc.arg(episode_id)::text
)
SELECT id::text AS id, received_at, revision::bigint AS revision,
 jsonb_build_object('episode_id',data->'episode_id','action',data->'action','previous_revision',data->'previous_revision',
  'triage',jsonb_build_object('revision',data#>'{triage,revision}','acknowledged',data#>'{triage,acknowledged}',
   'owned',data#>'{triage,owned}','snoozed_until',data#>'{triage,snoozed_until}','as_of',data#>'{triage,as_of}'),
  'trace_id',data->'trace_id')::jsonb AS payload
FROM receipts
WHERE revision <= sqlc.arg(snapshot_revision)::bigint
 AND (sqlc.arg(before_revision)::bigint = 0 OR revision < sqlc.arg(before_revision)::bigint)
ORDER BY revision DESC, id DESC LIMIT sqlc.arg(page_limit)::integer;

-- name: OperatorAlertWorkloadBuckets :many
WITH projected AS (
 SELECT e.id,e.started_at,e.severity,e.triage_acknowledged AS acknowledged,
  (e.triage_owner_id IS NOT NULL)::boolean AS owned,
  coalesce(e.triage_snoozed_until > sqlc.arg(now_at)::timestamptz,false)::boolean AS snoozed,
  CASE WHEN e.resolved_at IS NOT NULL THEN 'resolved'
   WHEN sqlc.arg(collection_enabled)::boolean AND c.last_success_at IS NOT NULL AND c.last_attempt_at=c.last_success_at
    AND c.last_success_at >= sqlc.arg(fresh_since)::timestamptz AND c.last_success_at <= sqlc.arg(future_before)::timestamptz
    AND e.last_checked_at=c.last_success_at AND e.last_evaluated_at >= sqlc.arg(fresh_since)::timestamptz
    AND e.last_evaluated_at <= sqlc.arg(future_before)::timestamptz THEN 'firing' ELSE 'unknown' END::text AS status
 FROM operator_alert_episodes e CROSS JOIN operator_alert_collection c
)
SELECT DISTINCT ON (status,severity,acknowledged,owned,snoozed)
 status,severity,acknowledged,owned,snoozed,
 (count(*) OVER (PARTITION BY status,severity,acknowledged,owned,snoozed))::bigint AS count,
 id AS oldest_id,started_at AS oldest_started_at
FROM projected
ORDER BY status,severity,acknowledged,owned,snoozed,started_at,id;

-- name: OperatorAlertResponseTimesEpisodes :many
SELECT e.id,e.severity,e.component,e.started_at,e.resolved_at,e.triage_revision,
 CASE WHEN e.resolved_at IS NOT NULL THEN 'resolved'
  WHEN sqlc.arg(collection_enabled)::boolean AND c.last_success_at IS NOT NULL AND c.last_attempt_at=c.last_success_at
   AND c.last_success_at>=sqlc.arg(fresh_since)::timestamptz AND c.last_success_at<=sqlc.arg(future_before)::timestamptz
   AND e.last_checked_at=c.last_success_at AND e.last_evaluated_at>=sqlc.arg(fresh_since)::timestamptz
   AND e.last_evaluated_at<=sqlc.arg(future_before)::timestamptz THEN 'firing' ELSE 'unknown' END::text AS status
FROM operator_alert_episodes e CROSS JOIN operator_alert_collection c
WHERE e.started_at>=sqlc.arg(since)::timestamptz AND e.started_at<=sqlc.arg(as_of)::timestamptz
ORDER BY e.started_at DESC,e.id DESC LIMIT sqlc.arg(row_limit)::integer;

-- name: OperatorAlertResponseTimesReceipts :many
SELECT (data->>'episode_id')::text AS episode_id,id::text AS id,received_at,
 jsonb_build_object('episode_id',data->'episode_id','action',data->'action','previous_revision',data->'previous_revision',
  'triage',jsonb_build_object('revision',data#>'{triage,revision}','acknowledged',data#>'{triage,acknowledged}',
   'owned',data#>'{triage,owned}','snoozed_until',data#>'{triage,snoozed_until}','as_of',data#>'{triage,as_of}'))::jsonb AS payload
FROM audit_log
WHERE kind='operator.action.alert_triage' AND data->>'episode_id'=ANY(sqlc.arg(episode_ids)::text[])
LIMIT sqlc.arg(row_limit)::integer;

-- name: OperatorAlertRecurrenceEpisodes :many
SELECT e.id,e.rule_key,e.series_id,e.name,e.severity,e.component,e.started_at,e.resolved_at,
 coalesce(e.account_id::text,'')::text AS account_id,coalesce(e.app_id::text,'')::text AS app_id,coalesce(e.node_id::text,'')::text AS node_id,
 CASE WHEN e.resolved_at IS NOT NULL THEN 'resolved'
  WHEN sqlc.arg(collection_enabled)::boolean AND c.last_success_at IS NOT NULL AND c.last_attempt_at=c.last_success_at
   AND c.last_success_at>=sqlc.arg(fresh_since)::timestamptz AND c.last_success_at<=sqlc.arg(future_before)::timestamptz
   AND e.last_checked_at=c.last_success_at AND e.last_evaluated_at>=sqlc.arg(fresh_since)::timestamptz
   AND e.last_evaluated_at<=sqlc.arg(future_before)::timestamptz THEN 'firing' ELSE 'unknown' END::text AS status
FROM operator_alert_episodes e CROSS JOIN operator_alert_collection c
WHERE e.started_at>=sqlc.arg(since)::timestamptz AND e.started_at<=sqlc.arg(as_of)::timestamptz
ORDER BY e.started_at DESC,e.id DESC LIMIT sqlc.arg(row_limit)::integer;

-- name: OperatorAlertInsertCollectionOutcome :exec
INSERT INTO operator_alert_collection_outcomes(attempted_at,outcome,reason)
VALUES (sqlc.arg(attempted_at)::timestamptz,sqlc.arg(outcome)::text,sqlc.arg(reason)::text);

-- name: OperatorAlertCollectionOutcomes :many
SELECT attempted_at,outcome,reason FROM operator_alert_collection_outcomes
WHERE attempted_at>=sqlc.arg(since)::timestamptz AND attempted_at<=sqlc.arg(as_of)::timestamptz
ORDER BY attempted_at LIMIT sqlc.arg(row_limit)::integer;

-- name: OperatorAlertPruneCollectionOutcomes :exec
DELETE FROM operator_alert_collection_outcomes WHERE attempted_at IN (
 SELECT attempted_at FROM operator_alert_collection_outcomes WHERE attempted_at<sqlc.arg(before)::timestamptz
 ORDER BY attempted_at LIMIT sqlc.arg(batch_size)::integer
);
