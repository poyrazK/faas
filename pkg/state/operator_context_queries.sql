-- name: OperatorContextRoot :one
SELECT 'deployment'::text AS kind, d.id::text AS id, d.status::text AS status, d.created_at AS at,
       a.account_id::text AS account_id, a.id::text AS app_id, ''::text AS node_id, ''::text AS job_id
FROM deployments d JOIN apps a ON a.id = d.app_id
WHERE sqlc.arg(kind)::text = 'deployment' AND d.id = sqlc.narg(resource_id)::uuid AND d.deleted_at IS NULL AND a.status <> 'deleted'
UNION ALL
SELECT 'job_run', r.id::text, r.aggregate_status::text, coalesce(r.finished_at,r.created_at),
       r.account_id::text, '', '', r.job_id::text
FROM job_runs r JOIN jobs j ON j.id = r.job_id AND j.account_id = r.account_id
WHERE sqlc.arg(kind)::text = 'job_run' AND r.id = sqlc.narg(resource_id)::uuid
UNION ALL
SELECT 'compute_node', n.id::text,
       CASE WHEN n.last_heartbeat_at < sqlc.arg(stale_before)::timestamptz THEN 'stale' ELSE n.lifecycle::text END::text,
       coalesce(n.drain_initiated_at,n.last_heartbeat_at,n.created_at), '', '', n.id::text, ''
FROM compute_nodes n WHERE sqlc.arg(kind)::text = 'compute_node' AND n.id = sqlc.narg(resource_id)::uuid;

-- name: OperatorContextAppOwner :one
SELECT account_id::text FROM apps WHERE id = sqlc.arg(app_id)::uuid AND status <> 'deleted';

-- name: OperatorContextNodeExists :one
SELECT EXISTS (SELECT 1 FROM compute_nodes WHERE id = sqlc.arg(node_id)::uuid)::boolean;

-- name: OperatorContextAccountExists :one
SELECT EXISTS (SELECT 1 FROM accounts WHERE id = sqlc.arg(account_id)::uuid)::boolean;

-- name: OperatorContextResources :one
-- Materialize the relationship set once: exact counts do not depend on the
-- returned page. App scope takes precedence over node scope. No account-wide
-- expansion and no recursive traversal through a shared node are permitted.
WITH scoped_apps AS MATERIALIZED (
    SELECT id, account_id, node_id, status FROM apps
    WHERE id = nullif(sqlc.arg(app_id)::text,'')::uuid AND status <> 'deleted'
    UNION
    SELECT id, account_id, node_id, status FROM apps
    WHERE sqlc.arg(app_id)::text = '' AND node_id = nullif(sqlc.arg(node_id)::text,'')::uuid AND status <> 'deleted'
      AND (sqlc.arg(account_id)::text = '' OR account_id = nullif(sqlc.arg(account_id)::text,'')::uuid)
    UNION
    SELECT a.id, a.account_id, a.node_id, a.status FROM apps a JOIN instances i ON i.app_id = a.id
    WHERE sqlc.arg(app_id)::text = '' AND i.node_id = nullif(sqlc.arg(node_id)::text,'')::uuid
      AND i.state IN ('waking','cold_booting','running','draining','warm') AND a.status <> 'deleted'
      AND (sqlc.arg(account_id)::text = '' OR a.account_id = nullif(sqlc.arg(account_id)::text,'')::uuid)
), scoped_jobs AS MATERIALIZED (
    SELECT id, account_id, status FROM jobs
    WHERE id = nullif(sqlc.arg(job_id)::text,'')::uuid AND account_id = nullif(sqlc.arg(account_id)::text,'')::uuid
    UNION
    SELECT j.id, j.account_id, j.status FROM jobs j JOIN instances i ON i.job_id = j.id
    WHERE sqlc.arg(app_id)::text = '' AND i.node_id = nullif(sqlc.arg(node_id)::text,'')::uuid
      AND i.kind = 'job_task' AND i.state IN ('waking','cold_booting','running','draining','warm') AND j.status <> 'deleted'
      AND (sqlc.arg(account_id)::text = '' OR j.account_id = nullif(sqlc.arg(account_id)::text,'')::uuid)
), scoped_nodes AS (
    SELECT nullif(sqlc.arg(node_id)::text,'')::uuid AS id
    UNION SELECT node_id FROM scoped_apps
    UNION SELECT i.node_id FROM instances i JOIN scoped_apps a ON a.id = i.app_id
          WHERE i.state IN ('waking','cold_booting','running','draining','warm')
    UNION SELECT i.node_id FROM instances i JOIN scoped_jobs j ON j.id = i.job_id
          WHERE i.kind = 'job_task' AND i.state IN ('waking','cold_booting','running','draining','warm')
), scoped_accounts AS (
    SELECT account_id AS id FROM scoped_apps
    UNION SELECT account_id FROM scoped_jobs
    UNION SELECT nullif(sqlc.arg(account_id)::text,'')::uuid
), resources AS MATERIALIZED (
    SELECT 'app'::text AS kind, a.id::text AS id, a.status::text AS status,
           CASE WHEN sqlc.arg(app_id)::text <> '' THEN 'incident_app'
                WHEN a.node_id = nullif(sqlc.arg(node_id)::text,'')::uuid THEN 'node_app_owner' ELSE 'node_live_instance' END::text AS relationship,
           a.account_id::text AS account_id, a.id::text AS app_id, false AS confirmed_failure
    FROM scoped_apps a
    UNION ALL
    SELECT 'deployment', d.id::text, d.status::text, 'app_deployment', a.account_id::text, a.id::text, d.status = 'failed'
    FROM scoped_apps a JOIN deployments d ON d.app_id = a.id
    WHERE d.deleted_at IS NULL AND (d.id = nullif(sqlc.arg(deployment_id)::text,'')::uuid OR d.status = 'live'
      OR d.created_at BETWEEN sqlc.arg(since)::timestamptz AND sqlc.arg(until)::timestamptz)
    UNION ALL
    SELECT 'compute_node', n.id::text, n.lifecycle::text,
           CASE WHEN n.id = nullif(sqlc.arg(node_id)::text,'')::uuid THEN 'incident_node' ELSE 'workload_node_relationship' END, '', '', false
    FROM scoped_nodes s JOIN compute_nodes n ON n.id = s.id
    UNION ALL
    SELECT 'tenant', a.id::text, a.status::text, 'account_owner', a.id::text, '', false
    FROM scoped_accounts s JOIN accounts a ON a.id = s.id
    UNION ALL
    SELECT 'job', j.id::text, j.status::text,
           CASE WHEN j.id = nullif(sqlc.arg(job_id)::text,'')::uuid THEN 'incident_job' ELSE 'node_job_instance' END,
           j.account_id::text, '', false FROM scoped_jobs j
    UNION ALL
    SELECT 'job_run', r.id::text, r.aggregate_status::text, 'same_job', r.account_id::text, '', r.aggregate_status IN ('failed','dead_letter')
    FROM job_runs r JOIN scoped_jobs j ON j.id = r.job_id AND j.account_id = r.account_id
    WHERE r.id = nullif(sqlc.arg(run_id)::text,'')::uuid OR coalesce(r.finished_at,r.created_at) BETWEEN sqlc.arg(since)::timestamptz AND sqlc.arg(until)::timestamptz
), page AS (
    SELECT * FROM resources WHERE (kind || '/' || id) COLLATE "C" > sqlc.arg(before_key)::text COLLATE "C"
    ORDER BY kind COLLATE "C", id COLLATE "C" LIMIT sqlc.arg(page_limit)::integer
)
SELECT count(*) FILTER (WHERE kind = 'app')::bigint AS apps,
       count(*) FILTER (WHERE kind = 'tenant')::bigint AS tenants,
       count(*) FILTER (WHERE kind = 'deployment')::bigint AS deployments,
       count(*) FILTER (WHERE kind = 'compute_node')::bigint AS nodes,
       count(*) FILTER (WHERE kind = 'job')::bigint AS jobs,
       count(*) FILTER (WHERE kind = 'job_run')::bigint AS job_runs,
       count(*) FILTER (WHERE confirmed_failure)::bigint AS failed_resources,
       coalesce((SELECT jsonb_agg(to_jsonb(p) ORDER BY p.kind COLLATE "C", p.id COLLATE "C") FROM page p),'[]'::jsonb)::jsonb AS resources
FROM resources;

-- name: OperatorContextTimeline :many
-- Bound each indexed source per verified target before merging. Account rows
-- are deliberately excluded: an account match is not incident correlation.
WITH targets AS (
    SELECT (t.value->>'kind')::text AS kind, (t.value->>'id')::uuid AS id
    FROM jsonb_array_elements(sqlc.arg(targets)::jsonb) AS t(value)
), history AS (
    SELECT t.kind AS resource_kind, t.id::text AS resource_id, h.*
    FROM targets t CROSS JOIN LATERAL (
        (SELECT e.id::text AS id, 'event'::text AS source, e.kind::text AS kind, ''::text AS status,
                e.at, coalesce(e.trace_id,'')::text AS trace_id, 'subject'::text AS match
         FROM events e WHERE e.subject = t.id AND e.at BETWEEN sqlc.arg(since)::timestamptz AND sqlc.arg(until)::timestamptz
         ORDER BY e.at DESC, e.id::text COLLATE "C" DESC LIMIT sqlc.arg(page_limit)::integer)
        UNION ALL
        (SELECT e.id::text, 'event', e.kind::text, '', e.at, coalesce(e.trace_id,''), 'operator_reference'
         FROM events e WHERE e.kind LIKE 'operator.action.%'
           AND coalesce(nullif(e.data->>'target_id',''), nullif(e.data->>'run_id',''), nullif(e.data->>'deployment_id','')) = t.id::text
           AND e.subject IS DISTINCT FROM t.id AND e.at BETWEEN sqlc.arg(since)::timestamptz AND sqlc.arg(until)::timestamptz
         ORDER BY e.at DESC, e.id::text COLLATE "C" DESC LIMIT sqlc.arg(page_limit)::integer)
        UNION ALL
        (SELECT a.id::text, 'deployment_audit', a.kind::text, '', a.at, '', 'deployment_id'
         FROM deployment_audit a WHERE t.kind = 'deployment' AND a.deployment_id = t.id
           AND a.at BETWEEN sqlc.arg(since)::timestamptz AND sqlc.arg(until)::timestamptz
         ORDER BY a.at DESC, a.id::text COLLATE "C" DESC LIMIT sqlc.arg(page_limit)::integer)
        UNION ALL
        (SELECT i.id::text, 'operator_intent', i.kind::text, i.status::text, i.requested_at, coalesce(i.trace_id,''), 'target_id'
         FROM operator_intents i WHERE i.target_id = t.id::text
           AND i.requested_at BETWEEN sqlc.arg(since)::timestamptz AND sqlc.arg(until)::timestamptz
         ORDER BY i.requested_at DESC, i.id::text COLLATE "C" DESC LIMIT sqlc.arg(page_limit)::integer)
    ) h
), unique_history AS (
    SELECT DISTINCT ON (source COLLATE "C",id COLLATE "C") * FROM history
    ORDER BY source COLLATE "C", id COLLATE "C", (match = 'subject') DESC, resource_kind COLLATE "C", resource_id COLLATE "C"
)
SELECT resource_kind, resource_id, id, source, kind, status, at, trace_id, match FROM unique_history
ORDER BY at DESC, source COLLATE "C" DESC, id COLLATE "C" DESC LIMIT sqlc.arg(page_limit)::integer;
