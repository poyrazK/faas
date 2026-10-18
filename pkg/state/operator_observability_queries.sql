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
