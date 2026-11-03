-- name: AddRequestAnalyticsMinutes :exec
-- ADR-20261103113952: add one apid flush of per-minute rollups. Row i of
-- the parallel arrays takes latency_hist from latency_hists[i*45+1 : i*45+45];
-- the CHECK on latency_hist rejects a flattened array of another width. The
-- update is additive, so apid replicas flushing the same key add up.
INSERT INTO request_analytics_minutes AS m (
    app_id, bucket_start, dimension_kind, dimension, method, status, deployment_id,
    account_id, requests, error_requests, cold_boots, request_time_ms, latency_max_ms, latency_hist
)
SELECT r.app_id, r.bucket_start, r.dimension_kind, r.dimension, r.method, r.status, r.deployment_id,
       r.account_id, r.requests, r.error_requests, r.cold_boots, r.request_time_ms, r.latency_max_ms,
       (sqlc.arg(latency_hists)::bigint[])[(r.ord - 1) * 45 + 1 : r.ord * 45]
FROM (
    SELECT unnest(sqlc.arg(app_ids)::uuid[]) AS app_id,
           unnest(sqlc.arg(bucket_starts)::timestamptz[]) AS bucket_start,
           unnest(sqlc.arg(dimension_kinds)::text[]) AS dimension_kind,
           unnest(sqlc.arg(dimensions)::text[]) AS dimension,
           unnest(sqlc.arg(methods)::text[]) AS method,
           unnest(sqlc.arg(statuses)::smallint[]) AS status,
           unnest(sqlc.arg(deployment_ids)::uuid[]) AS deployment_id,
           unnest(sqlc.arg(account_ids)::uuid[]) AS account_id,
           unnest(sqlc.arg(requests)::bigint[]) AS requests,
           unnest(sqlc.arg(error_requests)::bigint[]) AS error_requests,
           unnest(sqlc.arg(cold_boots)::bigint[]) AS cold_boots,
           unnest(sqlc.arg(request_time_ms)::bigint[]) AS request_time_ms,
           unnest(sqlc.arg(latency_max_ms)::int[]) AS latency_max_ms,
           generate_series(1, cardinality(sqlc.arg(app_ids)::uuid[])) AS ord
) AS r
ON CONFLICT (app_id, bucket_start, dimension_kind, dimension, method, status, deployment_id) DO UPDATE
SET requests = m.requests + EXCLUDED.requests,
    error_requests = m.error_requests + EXCLUDED.error_requests,
    cold_boots = m.cold_boots + EXCLUDED.cold_boots,
    request_time_ms = m.request_time_ms + EXCLUDED.request_time_ms,
    latency_max_ms = GREATEST(m.latency_max_ms, EXCLUDED.latency_max_ms),
    latency_hist = request_analytics_hist_add(m.latency_hist, EXCLUDED.latency_hist);

-- name: RecordRequestAnalyticsRollupCoverage :exec
-- The first flush that holds an app's rows records its minute. A later,
-- earlier minute (a replica's late flush) moves it back; an app that is
-- gone is skipped.
INSERT INTO request_analytics_rollup_coverage (app_id, covered_since)
SELECT a.id, c.covered_since
FROM (
    SELECT unnest(sqlc.arg(app_ids)::uuid[]) AS app_id,
           unnest(sqlc.arg(covered_since)::timestamptz[]) AS covered_since
) AS c
JOIN apps a ON a.id = c.app_id
ON CONFLICT (app_id) DO UPDATE
SET covered_since = LEAST(request_analytics_rollup_coverage.covered_since, EXCLUDED.covered_since)
WHERE EXCLUDED.covered_since < request_analytics_rollup_coverage.covered_since;

-- name: GetRequestAnalyticsRollupCoverage :one
SELECT covered_since FROM request_analytics_rollup_coverage WHERE app_id = $1;

-- name: RequestAnalyticsRollupGroups :many
-- ADR-20261103113952: analytics totals from the rollups. Groups are
-- (dimension, method) over the window, top sqlc.arg(group_limit) by
-- requests, the rest folded into one __other__ group; with hourly they are
-- also split by UTC hour (callers zero-fill). group_by 'status' groups the
-- route rows by HTTP status. The route/method filter applies to route
-- rows only, so callers pass group_by 'route' or 'status' with it. A
-- minute counts when it starts inside the window. latency_hist is the
-- groups' merged histogram; callers turn it into percentiles.
WITH filtered AS (
    SELECT CASE WHEN sqlc.arg(hourly)::bool THEN date_bin('1 hour', bucket_start, 'epoch'::timestamptz)
                ELSE 'epoch'::timestamptz END AS bucket,
           CASE WHEN sqlc.arg(group_by)::text = 'status' THEN status::text ELSE dimension END AS dimension,
           CASE WHEN sqlc.arg(group_by)::text = 'route' THEN method ELSE '' END AS method,
           requests, error_requests, cold_boots, request_time_ms, latency_max_ms, latency_hist
    FROM request_analytics_minutes
    WHERE app_id = sqlc.arg(app_id)
      AND account_id = sqlc.arg(account_id)
      AND dimension_kind = CASE WHEN sqlc.arg(group_by)::text = 'status' THEN 'route' ELSE sqlc.arg(group_by)::text END
      AND bucket_start >= sqlc.arg(window_from)::timestamptz
      AND bucket_start <  sqlc.arg(window_until)::timestamptz
      AND (sqlc.arg(route)::text = '' OR dimension = sqlc.arg(route)::text)
      AND (sqlc.arg(method)::text = '' OR method = sqlc.arg(method)::text)
), top_groups AS (
    SELECT dimension, method
    FROM filtered
    GROUP BY dimension, method
    ORDER BY SUM(requests) DESC, dimension ASC, method ASC
    LIMIT sqlc.arg(group_limit)::int
)
SELECT f.bucket::timestamptz AS bucket,
       COALESCE(t.dimension, '__other__')::text AS dimension,
       COALESCE(t.method, '')::text AS method,
       SUM(f.requests)::bigint AS requests,
       SUM(f.error_requests)::bigint AS error_requests,
       SUM(f.cold_boots)::bigint AS cold_boots,
       SUM(f.request_time_ms)::bigint AS request_time_ms,
       MAX(f.latency_max_ms)::int AS latency_max_ms,
       request_analytics_hist_sum(f.latency_hist)::bigint[] AS latency_hist
FROM filtered AS f
LEFT JOIN top_groups AS t ON t.dimension = f.dimension AND t.method = f.method
GROUP BY f.bucket, t.dimension, t.method
ORDER BY 1 ASC, 4 DESC, 2 ASC, 3 ASC;
