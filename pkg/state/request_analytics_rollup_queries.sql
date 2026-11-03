-- name: AddRequestAnalyticsMinutes :exec
-- ADR-20261103113952: add one apid flush of per-minute rollups. Row i of
-- the parallel arrays takes latency_hist from latency_hists[i*45+1 : i*45+45];
-- the CHECK on latency_hist rejects a flattened array of another width. The
-- update is additive, so apid replicas flushing the same key add up.
INSERT INTO request_analytics_minutes AS m (
    app_id, bucket_start, dimension_kind, dimension, method, status, deployment_id,
    account_id, requests, error_requests, cold_boots, latency_max_ms, latency_hist
)
SELECT r.app_id, r.bucket_start, r.dimension_kind, r.dimension, r.method, r.status, r.deployment_id,
       r.account_id, r.requests, r.error_requests, r.cold_boots, r.latency_max_ms,
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
           unnest(sqlc.arg(latency_max_ms)::int[]) AS latency_max_ms,
           generate_series(1, cardinality(sqlc.arg(app_ids)::uuid[])) AS ord
) AS r
ON CONFLICT (app_id, bucket_start, dimension_kind, dimension, method, status, deployment_id) DO UPDATE
SET requests = m.requests + EXCLUDED.requests,
    error_requests = m.error_requests + EXCLUDED.error_requests,
    cold_boots = m.cold_boots + EXCLUDED.cold_boots,
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
