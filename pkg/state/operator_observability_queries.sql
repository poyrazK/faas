-- name: OperatorQueuedBuildsCount :one
-- Deferred builds that are not due yet (ADR-20261021221906) are not backlog.
SELECT count(*)::bigint FROM builds
WHERE status = 'queued' AND (not_before IS NULL OR not_before <= now());

-- name: OperatorQueuedBuildsSnapshot :one
SELECT count(*)::bigint AS queued,
       min(coalesce(not_before, enqueued_at))::timestamptz AS oldest_enqueued_at
FROM builds WHERE status = 'queued' AND (not_before IS NULL OR not_before <= now());

-- name: OperatorBuildRecoverySnapshot :one
WITH running AS (
    SELECT count(*)::bigint AS running,
           count(*) FILTER (WHERE started_at IS NULL)::bigint AS missing_started_at,
           min(started_at)::timestamptz AS oldest_started_at
    FROM builds WHERE status = 'running'
), cleanup AS (
    SELECT count(*)::bigint AS pending_cleanup,
           min(created_at)::timestamptz AS oldest_cleanup_created_at,
           count(*) FILTER (WHERE last_error IS NOT NULL)::bigint AS retried_cleanup
    FROM builder_vm_cleanup
)
SELECT running.*, cleanup.* FROM running CROSS JOIN cleanup;

-- Same eligibility and owner compatibility as ListBuildsAwaitingImage, without
-- its recovery batch limit. Checkpointed preparations are active/resumable work,
-- not unconsumed build handoffs.
-- name: OperatorBuildImageHandoffSnapshot :one
SELECT count(*)::bigint AS pending,
       count(*) FILTER (WHERE b.finished_at IS NULL OR NOT isfinite(b.finished_at))::bigint AS missing_finished_at,
       min(b.finished_at)::timestamptz AS oldest_finished_at
FROM deployments d JOIN builds b ON b.deployment_id = d.id
JOIN build_provenance p ON p.build_id = b.id
WHERE d.status IN ('pending', 'building') AND b.status = 'succeeded'
  AND COALESCE(d.rootfs_path, '') <> ''
  AND NOT EXISTS (SELECT 1 FROM deployment_image_preparations i WHERE i.deployment_id = d.id)
  AND (sqlc.arg(node_id)::text = '' OR COALESCE(p.builder_node_id, '') = '' OR p.builder_node_id = sqlc.arg(node_id)::text);

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
INSERT INTO operator_alert_collection_outcomes(attempted_at,outcome,reason,diagnostics)
VALUES (sqlc.arg(attempted_at)::timestamptz,sqlc.arg(outcome)::text,sqlc.arg(reason)::text,sqlc.narg(diagnostics)::jsonb);

-- name: OperatorAlertCollectionFailures :many
SELECT attempted_at,reason,diagnostics FROM operator_alert_collection_outcomes
WHERE outcome='failure' AND attempted_at>=sqlc.arg(since)::timestamptz AND attempted_at<=sqlc.arg(as_of)::timestamptz
ORDER BY attempted_at DESC LIMIT sqlc.arg(row_limit)::integer;

-- name: OperatorAlertCollectionOutcomes :many
SELECT attempted_at,outcome,reason FROM operator_alert_collection_outcomes
WHERE attempted_at>=sqlc.arg(since)::timestamptz AND attempted_at<=sqlc.arg(as_of)::timestamptz
ORDER BY attempted_at LIMIT sqlc.arg(row_limit)::integer;

-- name: OperatorAlertPruneCollectionOutcomes :exec
DELETE FROM operator_alert_collection_outcomes WHERE attempted_at IN (
 SELECT attempted_at FROM operator_alert_collection_outcomes WHERE attempted_at<sqlc.arg(before)::timestamptz
 ORDER BY attempted_at LIMIT sqlc.arg(batch_size)::integer
);

-- name: OperatorImagePreparationProgressSnapshot :many
SELECT p.phase, count(*)::bigint AS pending,
 count(*) FILTER (WHERE p.phase_started_at IS NULL)::bigint AS unknown_age,
 count(*) FILTER (WHERE NOT isfinite(p.phase_started_at) OR p.phase_started_at <= 'epoch'::timestamptz)::bigint AS invalid_age,
 min(p.phase_started_at)::timestamptz AS oldest_phase_started_at
FROM deployment_image_preparations p JOIN deployments d ON d.id=p.deployment_id
WHERE p.phase <> 'handed_off' AND d.status IN ('pending','building','imaging','snapshotting')
 AND (sqlc.arg(node_id)::text='' OR p.node_name='' OR p.node_name=sqlc.arg(node_id)::text)
GROUP BY p.phase;

-- name: OperatorSnapshotPrimeSnapshot :many
WITH scoped AS MATERIALIZED (
 SELECT d.* FROM deployments d JOIN apps a ON a.id=d.app_id
 WHERE d.status='snapshotting' AND a.status<>'deleted' AND NOT d.environment_workload_held
  AND (sqlc.arg(node_id)::text='' OR a.node_id IS NULL OR a.node_id::text=sqlc.arg(node_id)::text)
  AND coalesce(d.stage_state->>'current','')<>'readiness'
), timed AS MATERIALIZED (
 SELECT d.*,
  CASE WHEN d.stage_state->>'current'='snapshot_prepare'
   AND (d.stage_state->>'current_started_at') ~ '^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d{1,9})?(Z|[+-]\d{2}:\d{2})$'
   AND pg_input_is_valid(d.stage_state->>'current_started_at','timestamp with time zone')
   THEN (d.stage_state->>'current_started_at')::timestamptz END AS stage_at
 FROM scoped d
), facts AS (
 SELECT d.id,d.stage_at,d.rootfs_key,
  (cardinality(d.release_command)=0 OR coalesce(t.status='succeeded' AND t.app_id=d.app_id
   AND t.artifact_key=d.rootfs_key AND t.image_digest=d.image_digest AND t.deployment_scope=d.scope
   AND (t.command_shell=d.release_command_shell AND t.command=d.release_command
    OR NOT t.command_shell AND scenario_rehearsal_release_matches(t.command,d.id,d.release_command,d.release_command_shell)),false)) AS release_allowed,
  cardinality(d.release_command)>0 AS release_required,t.finished_at AS release_at,
  sn.created_at AS snapshot_at,c.changed_at,
  coalesce(ins.active,false) AS active,coalesce(ins.parked,false) AS parked,
  coalesce(ins.bad_time,false) AS bad_time,coalesce(ins.unknown_activity,false) AS unknown_activity,
  ins.activity_at
 FROM timed d
 LEFT JOIN app_tasks t ON t.deployment_id=d.id AND t.kind='release'
 LEFT JOIN LATERAL (SELECT s.created_at FROM snapshots s WHERE s.deployment_id=d.id AND NOT s.stale
  ORDER BY (s.tier='warm') DESC,s.created_at DESC LIMIT 1) sn ON true
 LEFT JOIN LATERAL (SELECT max(b.changed_at) AS changed_at FROM (
  SELECT changed_at FROM app_runtime_config_changes WHERE app_id=d.app_id
  UNION ALL SELECT changed_at FROM app_runtime_config_scope_changes WHERE app_id=d.app_id AND scope IN ('default',d.scope)
 ) b) c ON true
 LEFT JOIN LATERAL (
  SELECT bool_or(i.state IN ('waking','cold_booting','running','draining','snapshotting','migrating','warm','frozen')) AS active,
   bool_or(i.state='parked' AND CASE WHEN r.instance_id IS NOT NULL THEN
     r.scope=d.scope AND r.boundary_at>=coalesce(c.changed_at,'epoch'::timestamptz)
     AND environment_runtime_inputs_fresh(d.app_id,r.scope,r.boundary_at,r.variables,r.secret_versions,r.all_secrets,r.secret_refs,r.sidecar_secret_versions)
    ELSE NOT environment_runtime_receipt_required(d.app_id,d.scope)
     AND least(i.started_at,CASE WHEN substr(i.wake_id::text,15,1)='7' THEN
       to_timestamp(('x'||substr(replace(i.wake_id::text,'-',''),1,12))::bit(48)::bigint::double precision/1000)
       ELSE i.started_at END)>coalesce(c.changed_at,'epoch'::timestamptz) END) AS parked,
   bool_or(i.started_at IS NULL OR NOT isfinite(i.started_at) OR i.started_at>statement_timestamp()
    OR (i.terminal_at IS NOT NULL AND (NOT isfinite(i.terminal_at) OR i.terminal_at>statement_timestamp()))
    OR (i.parked_at IS NOT NULL AND (NOT isfinite(i.parked_at) OR i.parked_at>statement_timestamp()))) AS bad_time,
   bool_or(CASE WHEN i.state='parked' THEN i.parked_at IS NULL
    WHEN i.state NOT IN ('waking','cold_booting','running','draining','snapshotting','migrating','warm','frozen') THEN i.terminal_at IS NULL ELSE false END) AS unknown_activity,
   max(greatest(i.terminal_at,i.parked_at)) AS activity_at
  FROM (SELECT * FROM instances WHERE app_id=d.app_id ORDER BY started_at DESC LIMIT sqlc.arg(instance_lookback)::integer) i
  LEFT JOIN instance_runtime_config_receipts r ON r.instance_id=i.id AND r.wake_id=i.wake_id
  WHERE i.deployment_id=d.id AND (i.started_at>d.stage_at OR i.started_at IS NULL)
 ) ins ON true
), classified AS (
 SELECT CASE
  WHEN stage_at IS NULL OR NOT isfinite(stage_at) OR stage_at<='epoch'::timestamptz OR stage_at>statement_timestamp() OR rootfs_key='' THEN 'unknown'
  WHEN NOT release_allowed THEN 'release_blocked'
  WHEN snapshot_at>statement_timestamp() THEN 'unknown'
  WHEN snapshot_at>stage_at THEN 'snapshot_recorded'
  WHEN bad_time THEN 'unknown'
  WHEN active THEN 'active'
  WHEN parked THEN 'publication_pending'
  ELSE 'waiting' END::text AS class,
  CASE WHEN NOT release_allowed OR (release_required AND (release_at IS NULL OR NOT isfinite(release_at) OR release_at<='epoch'::timestamptz OR release_at>statement_timestamp()))
     OR (unknown_activity AND NOT coalesce(snapshot_at>stage_at,false)) OR changed_at>statement_timestamp() THEN NULL
   ELSE greatest(stage_at,CASE WHEN release_required THEN release_at END,changed_at,CASE WHEN snapshot_at>stage_at THEN NULL ELSE activity_at END) END AS since
 FROM facts
)
SELECT class,count(*)::bigint AS pending,
 count(*) FILTER (WHERE class='unknown' OR since IS NULL)::bigint AS unknown_age,
 min(CASE WHEN class<>'unknown' THEN since END)::timestamptz AS oldest_at
FROM classified GROUP BY class;

-- name: OperatorPostCaptureSnapshot :many
WITH scoped AS MATERIALIZED (
 SELECT d.id,d.app_id,d.scope,d.rootfs_key,d.stage_state FROM deployments d JOIN apps a ON a.id=d.app_id
 WHERE d.status='snapshotting' AND a.status<>'deleted' AND NOT d.environment_workload_held
 AND coalesce(d.stage_state->>'current','')<>'readiness'
 AND NOT EXISTS (SELECT 1 FROM app_secrets s WHERE s.app_id=d.app_id AND s.scope=d.scope AND s.secret_class='ephemeral')
), timed AS MATERIALIZED (
 SELECT d.*,CASE WHEN (d.stage_state->>'current_started_at') ~ '^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d{1,9})?(Z|[+-]\d{2}:\d{2})$' AND pg_input_is_valid(d.stage_state->>'current_started_at','timestamp with time zone') THEN (d.stage_state->>'current_started_at')::timestamptz END AS stage_at,
  CASE WHEN (d.stage_state->'hosting_verification'->>'started_at') ~ '^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d{1,9})?(Z|[+-]\d{2}:\d{2})$' AND pg_input_is_valid(d.stage_state->'hosting_verification'->>'started_at','timestamp with time zone') THEN (d.stage_state->'hosting_verification'->>'started_at')::timestamptz END AS hosting_started,CASE WHEN (d.stage_state->'hosting_verification'->>'deadline_at') ~ '^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d{1,9})?(Z|[+-]\d{2}:\d{2})$' AND pg_input_is_valid(d.stage_state->'hosting_verification'->>'deadline_at','timestamp with time zone') THEN (d.stage_state->'hosting_verification'->>'deadline_at')::timestamptz END AS hosting_deadline,
  CASE WHEN (d.stage_state->'hosting_verification'->>'completed_at') ~ '^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d{1,9})?(Z|[+-]\d{2}:\d{2})$' AND pg_input_is_valid(d.stage_state->'hosting_verification'->>'completed_at','timestamp with time zone') THEN (d.stage_state->'hosting_verification'->>'completed_at')::timestamptz END AS hosting_completed,CASE WHEN (d.stage_state->'hosting_verification'->>'retry_not_before') ~ '^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d{1,9})?(Z|[+-]\d{2}:\d{2})$' AND pg_input_is_valid(d.stage_state->'hosting_verification'->>'retry_not_before','timestamp with time zone') THEN (d.stage_state->'hosting_verification'->>'retry_not_before')::timestamptz END AS hosting_retry,
  CASE WHEN pg_input_is_valid(d.stage_state->'hosting_verification'->>'attempts','integer') THEN (d.stage_state->'hosting_verification'->>'attempts')::integer END AS hosting_attempts
 FROM scoped d
), facts AS (
 SELECT d.*,sn.created_at AS published_at,h.first_held_at AS held_at,h.observed_at AS hold_observed,g.started_at AS gate_started,g.deadline_at AS gate_deadline,g.status AS gate_status,
  park.present AS parked,park.bad AS bad_park,park.missing AS missing_park,park.oldest AS parked_at
 FROM timed d
 LEFT JOIN LATERAL (SELECT created_at FROM snapshots WHERE deployment_id=d.id AND tier='init' AND NOT stale ORDER BY created_at DESC LIMIT 1) sn ON true
 LEFT JOIN deployment_mcp_task_holds h ON h.deployment_id=d.id AND (h.first_held_at>=d.stage_at OR h.observed_at>=d.stage_at)
 LEFT JOIN deployment_dependency_gates g ON g.deployment_id=d.id AND g.started_at>=d.stage_at
 LEFT JOIN LATERAL (SELECT max(b.changed_at) AS changed_at FROM (
  SELECT changed_at FROM app_runtime_config_changes WHERE app_id=d.app_id
  UNION ALL SELECT changed_at FROM app_runtime_config_scope_changes WHERE app_id=d.app_id AND scope IN ('default',d.scope)
 ) b) boundary ON true
 LEFT JOIN LATERAL (
  SELECT count(*)>0 AS present,
   bool_or(i.started_at IS NULL OR NOT isfinite(i.started_at) OR i.started_at>statement_timestamp() OR i.parked_at>statement_timestamp() OR boundary.changed_at>statement_timestamp()) AS bad,
   bool_or(i.parked_at IS NULL OR i.parked_at<='epoch'::timestamptz OR i.parked_at<i.started_at) AS missing,
   min(i.parked_at) AS oldest
  FROM (SELECT * FROM instances WHERE app_id=d.app_id ORDER BY started_at DESC LIMIT sqlc.arg(instance_lookback)::integer) i
  LEFT JOIN instance_runtime_config_receipts r ON r.instance_id=i.id AND r.wake_id=i.wake_id
  WHERE i.deployment_id=d.id AND (i.started_at>d.stage_at OR i.started_at IS NULL) AND i.state='parked' AND i.mode NOT IN ('worker','job')
   AND (i.started_at IS NULL OR i.started_at>statement_timestamp() OR i.parked_at>statement_timestamp() OR boundary.changed_at>statement_timestamp() OR CASE WHEN r.instance_id IS NOT NULL THEN r.scope=d.scope AND r.boundary_at>=coalesce(boundary.changed_at,'epoch'::timestamptz)
    AND environment_runtime_inputs_fresh(d.app_id,r.scope,r.boundary_at,r.variables,r.secret_versions,r.all_secrets,r.secret_refs,r.sidecar_secret_versions)
   ELSE NOT environment_runtime_receipt_required(d.app_id,d.scope)
    AND least(i.started_at,CASE WHEN substr(i.wake_id::text,15,1)='7' THEN
     to_timestamp(('x'||substr(replace(i.wake_id::text,'-',''),1,12))::bit(48)::bigint::double precision/1000) ELSE i.started_at END)>coalesce(boundary.changed_at,'epoch'::timestamptz) END)
 ) park ON NOT coalesce(sn.created_at>d.stage_at,false)
), classified AS (
 SELECT CASE
  WHEN coalesce(stage_state->>'current','')<>'snapshot_prepare' OR stage_at IS NULL OR stage_at<='epoch'::timestamptz OR stage_at>statement_timestamp() OR rootfs_key='' THEN 'unknown'
  WHEN published_at>statement_timestamp() THEN 'unknown'
  WHEN published_at>stage_at THEN CASE
   WHEN held_at IS NOT NULL THEN CASE WHEN NOT isfinite(held_at) OR held_at>statement_timestamp() OR NOT isfinite(hold_observed) OR hold_observed>statement_timestamp() THEN 'unknown' ELSE 'compatibility_held' END
   WHEN gate_started IS NOT NULL AND (NOT isfinite(gate_started) OR gate_started>statement_timestamp() OR gate_deadline IS NULL OR NOT isfinite(gate_deadline) OR gate_deadline<=gate_started) THEN 'unknown'
   WHEN gate_status IN ('waiting','failed') THEN CASE WHEN gate_status='failed' OR gate_deadline<=statement_timestamp() THEN 'gate_overdue' ELSE 'dependency_held' END
   WHEN stage_state->'hosting_verification' IS NOT NULL AND stage_state->'hosting_verification'<>'null'::jsonb AND (hosting_started IS NULL OR hosting_started<='epoch'::timestamptz OR hosting_started>=stage_at) THEN CASE
    WHEN hosting_started IS NULL OR hosting_started<='epoch'::timestamptz OR hosting_started>statement_timestamp() OR hosting_deadline IS NULL OR hosting_deadline<=hosting_started OR coalesce(hosting_attempts,0)<=0
     OR (stage_state->'hosting_verification'->>'completed_at' IS NOT NULL AND (hosting_completed IS NULL OR hosting_completed<hosting_started OR hosting_completed>statement_timestamp() OR coalesce(stage_state->'hosting_verification'->>'last_error_code','')<>'' OR stage_state->'hosting_verification'->>'retry_not_before' IS NOT NULL)) THEN 'unknown'
    WHEN hosting_completed IS NOT NULL THEN 'activation_pending'
    WHEN coalesce(stage_state->'hosting_verification'->>'last_error_code','') IN ('smoke_authorization_unavailable','smoke_gateway_unavailable','smoke_transport_unavailable','smoke_deployment_mismatch','smoke_response_unproven','smoke_contract_unavailable') THEN CASE
     WHEN stage_state->'hosting_verification'->>'retry_not_before' IS NOT NULL AND (hosting_retry IS NULL OR hosting_retry<hosting_started OR hosting_retry>hosting_deadline) THEN 'unknown'
     WHEN hosting_deadline<=statement_timestamp() THEN 'gate_overdue' ELSE 'hosting_retry' END
    WHEN coalesce(stage_state->'hosting_verification'->>'last_error_code','')<>'' THEN 'unknown'
    ELSE 'hosting_verifying' END
   ELSE 'activation_pending' END
  WHEN coalesce(parked,false) THEN CASE WHEN bad_park THEN 'unknown' ELSE 'publication_pending' END
  ELSE NULL END::text AS class,* FROM facts
), aged AS (
 SELECT class,CASE class
  WHEN 'publication_pending' THEN CASE WHEN NOT missing_park THEN parked_at END
  WHEN 'activation_pending' THEN published_at
  WHEN 'compatibility_held' THEN CASE WHEN held_at>=stage_at THEN greatest(published_at,held_at) END
  WHEN 'dependency_held' THEN greatest(published_at,gate_started)
  WHEN 'hosting_verifying' THEN greatest(published_at,hosting_started)
  WHEN 'hosting_retry' THEN greatest(published_at,hosting_started)
  WHEN 'gate_overdue' THEN greatest(published_at,CASE WHEN gate_status='failed' THEN gate_started WHEN gate_status='waiting' THEN gate_deadline ELSE hosting_deadline END)
 END AS since FROM classified WHERE class IS NOT NULL
)
SELECT class,count(*)::bigint AS pending,count(*) FILTER (WHERE since IS NULL)::bigint AS unknown_age,min(since)::timestamptz AS oldest_at
FROM aged GROUP BY class;

-- Shared fleet app-task claim queue; no batch cap or owner filter. Keep the
-- queued admission predicates aligned with ClaimNextUnfencedAppTask. Active
-- tasks on deleted apps remain visible until teardown/lease recovery completes.
-- name: OperatorAppTaskSnapshot :many
WITH evidence AS (
 SELECT task.*, app.status AS app_status,
  app.managed_postgres_admission_cutover_id IS NOT NULL AS admission_held,
  (task.exclusive_operation_id IS NULL OR EXISTS (
   SELECT 1 FROM exclusive_work_operations operation
   WHERE operation.id=task.exclusive_operation_id AND operation.account_id=task.account_id
    AND operation.app_id=task.app_id AND operation.state='running'
    AND operation.generation=task.exclusive_generation
    AND operation.lease_expires_at>sqlc.arg(observed_at)::timestamptz
    AND operation.attempt_deadline>sqlc.arg(observed_at))) AS exclusive_current,
  EXISTS (SELECT 1 FROM schedule_occurrences occurrence
   WHERE occurrence.id=task.occurrence_id AND occurrence.started_at IS NOT NULL) AS occurrence_started
 FROM app_tasks task JOIN apps app ON app.id=task.app_id
 WHERE task.status NOT IN ('succeeded','failed','timed_out','cancelled')
), classified AS (
 SELECT *, CASE
  WHEN NOT isfinite(created_at) OR created_at<='epoch' OR created_at>sqlc.arg(observed_at)
   OR NOT isfinite(updated_at) OR updated_at<created_at OR updated_at>sqlc.arg(observed_at)
   OR finished_at IS NOT NULL
   OR (retry_at IS NOT NULL AND (NOT isfinite(retry_at) OR retry_at<='epoch'))
   OR (start_deadline_at IS NOT NULL AND (NOT isfinite(start_deadline_at) OR start_deadline_at<='epoch'))
   OR (cancel_requested_at IS NOT NULL AND (NOT isfinite(cancel_requested_at)
    OR cancel_requested_at<created_at OR cancel_requested_at>sqlc.arg(observed_at))) THEN 'unknown'
  WHEN status='queued' THEN CASE
   WHEN lease_token IS NOT NULL OR lease_owner IS NOT NULL OR lease_expires_at IS NOT NULL
    OR started_at IS NOT NULL THEN 'unknown'
   WHEN cancel_requested_at IS NOT NULL THEN 'cancellation_pending'
   WHEN start_deadline_at<sqlc.arg(observed_at) AND NOT occurrence_started THEN 'deadline_overdue'
   WHEN admission_held OR NOT exclusive_current OR app_status='deleted' THEN 'held'
   WHEN retry_at>sqlc.arg(observed_at) THEN 'retry_wait'
   ELSE 'queued' END
  WHEN status NOT IN ('restoring','running') OR lease_token IS NULL OR lease_owner IS NULL
   OR lease_owner='' OR lease_expires_at IS NULL OR NOT isfinite(lease_expires_at) OR lease_expires_at<=created_at
   OR (status='restoring' AND started_at IS NOT NULL)
   OR (status='running' AND (started_at IS NULL OR NOT isfinite(started_at)
    OR started_at<created_at OR started_at>sqlc.arg(observed_at))) THEN 'unknown'
  WHEN lease_expires_at<=sqlc.arg(observed_at) THEN 'lease_expired'
  WHEN cancel_requested_at IS NOT NULL THEN 'cancellation_pending'
  ELSE status END::text AS class
 FROM evidence
), timed AS (
 SELECT kind,class,CASE class
  WHEN 'queued' THEN greatest(created_at,updated_at,retry_at)
  WHEN 'deadline_overdue' THEN start_deadline_at
  WHEN 'running' THEN started_at
  WHEN 'cancellation_pending' THEN cancel_requested_at
  WHEN 'lease_expired' THEN lease_expires_at
  ELSE NULL END::timestamptz AS boundary
 FROM classified
)
SELECT kind::text,class::text,count(*)::bigint AS pending,
 count(*) FILTER (WHERE boundary IS NULL)::bigint AS unknown_age,
 min(boundary)::timestamptz AS oldest_at
FROM timed GROUP BY kind,class;

-- No execution list/page cap. Include disabled and suspended command crons as
-- separate intentional cohorts. Nonempty ownership matches ListOwnedCrons;
-- legacy listing also applies Engine.ownsAppDecision's local fallback.
-- name: OperatorCommandCronObservations :many
SELECT c.schedule,c.timezone,c.created_at,c.last_fired_at,c.enabled,c.skip_if_running,c.schedule_policy,
 (c.suspended_reason<>'')::boolean AS suspended,(a.status='deleted')::boolean AS app_deleted,
 (account.status IN ('active','past_due') AND account.abuse_hold_at IS NULL)::boolean AS account_active,
 EXISTS(SELECT 1 FROM deployments d WHERE d.app_id=c.app_id AND d.status='live'
  AND coalesce(d.rootfs_key,'')<>'' AND d.image_digest<>'')::boolean AS live_deployment,
 EXISTS(SELECT 1 FROM app_tasks task WHERE task.cron_id=c.id AND task.status IN ('queued','restoring','running'))::boolean AS active_tasks,
 EXISTS(SELECT 1 FROM app_tasks task WHERE task.cron_id=c.id AND task.status IN ('queued','restoring','running')
  AND task.exclusive_operation_id IS NULL)::boolean AS active_ordinary_tasks,
 (binding.trigger_id IS NOT NULL)::boolean AS exclusive_bound,
 (binding.trigger_id IS NULL OR binding.app_id=c.app_id)::boolean AS binding_valid
FROM crons c JOIN apps a ON a.id=c.app_id JOIN accounts account ON account.id=a.account_id
LEFT JOIN exclusive_work_trigger_bindings binding ON binding.source='cron' AND binding.trigger_id=c.id AND binding.account_id=a.account_id
WHERE cardinality(c.command)>0 AND
 ((sqlc.arg(owner_node)::text<>'' AND a.node_id::text=sqlc.arg(owner_node))
  OR (sqlc.arg(owner_node)::text='' AND
   (sqlc.arg(local_node)::text='' OR a.node_id IS NULL OR a.node_id::text=sqlc.arg(local_node))));
