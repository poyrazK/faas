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
    latency_hist = request_analytics_hist_add(COALESCE(m.latency_hist, array_fill(0::bigint, ARRAY[45])), EXCLUDED.latency_hist);

-- name: AddRequestAnalyticsHours :exec
-- ADR-20261103113952 amendment 2: the same flush folded to UTC hours, in
-- the same shape as AddRequestAnalyticsMinutes.
INSERT INTO request_analytics_hours AS m (
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
    latency_hist = request_analytics_hist_add(COALESCE(m.latency_hist, array_fill(0::bigint, ARRAY[45])), EXCLUDED.latency_hist);

-- name: RecordRequestAnalyticsRollupCoverage :exec
-- The first flush that holds an app's rows records its minute, for both
-- tiers. A later, earlier minute (a replica's late flush) moves either back;
-- an app whose coverage predates the hourly tier gets hours_covered_since
-- from its first flush after it. An app that is gone is skipped.
INSERT INTO request_analytics_rollup_coverage AS c (app_id, covered_since, hours_covered_since)
SELECT a.id, n.covered_since, n.covered_since
FROM (
    SELECT unnest(sqlc.arg(app_ids)::uuid[]) AS app_id,
           unnest(sqlc.arg(covered_since)::timestamptz[]) AS covered_since
) AS n
JOIN apps a ON a.id = n.app_id
ON CONFLICT (app_id) DO UPDATE
SET covered_since = LEAST(c.covered_since, EXCLUDED.covered_since),
    hours_covered_since = LEAST(COALESCE(c.hours_covered_since, EXCLUDED.hours_covered_since), EXCLUDED.hours_covered_since)
WHERE EXCLUDED.covered_since < c.covered_since
   OR c.hours_covered_since IS NULL
   OR EXCLUDED.hours_covered_since < c.hours_covered_since;

-- name: GetRequestAnalyticsRollupCoverage :one
SELECT covered_since, hours_covered_since FROM request_analytics_rollup_coverage WHERE app_id = $1;

-- name: RequestAnalyticsRollupGroups :many
-- ADR-20261103113952: analytics totals from the rollups. Whole hours in
-- [hours_from, hours_until) come from the hourly tier and the minutes on
-- either side from the minute tier (amendment 2); callers pass an empty
-- hour range to read minutes only. Groups are (dimension, method) over the
-- window, top sqlc.arg(group_limit) by requests, the rest folded into one
-- __other__ group; with hourly they are also split by UTC hour (callers
-- zero-fill). group_by 'status' groups the route rows by HTTP status. The
-- route/method filter applies to route rows only, so callers pass group_by
-- 'route' or 'status' with it. A bucket counts when it starts inside the
-- window. latency_hist is the groups' merged histogram, summed bucket by
-- bucket; callers turn it into percentiles. A row holds its histogram in
-- latency_hist, latency_hist_int4 or, while two releases write, both
-- (amendment 3), so each bucket adds the two. filtered is not materialized:
-- the top-group pass reads no histograms.
WITH filtered AS NOT MATERIALIZED (
    SELECT bucket_start, dimension_kind, dimension, method, status,
           requests, error_requests, cold_boots, request_time_ms, latency_max_ms, latency_hist, latency_hist_int4
    FROM request_analytics_minutes AS rm
    WHERE rm.app_id = sqlc.arg(app_id)
      AND rm.account_id = sqlc.arg(account_id)
      AND rm.dimension_kind = CASE WHEN sqlc.arg(group_by)::text = 'status' THEN 'route' ELSE sqlc.arg(group_by)::text END
      AND ((rm.bucket_start >= sqlc.arg(window_from)::timestamptz AND rm.bucket_start < sqlc.arg(hours_from)::timestamptz)
        OR (rm.bucket_start >= sqlc.arg(hours_until)::timestamptz AND rm.bucket_start < sqlc.arg(window_until)::timestamptz))
    UNION ALL
    SELECT bucket_start, dimension_kind, dimension, method, status,
           requests, error_requests, cold_boots, request_time_ms, latency_max_ms, latency_hist, latency_hist_int4
    FROM request_analytics_hours AS rh
    WHERE rh.app_id = sqlc.arg(app_id)
      AND rh.account_id = sqlc.arg(account_id)
      AND rh.dimension_kind = CASE WHEN sqlc.arg(group_by)::text = 'status' THEN 'route' ELSE sqlc.arg(group_by)::text END
      AND rh.bucket_start >= sqlc.arg(hours_from)::timestamptz
      AND rh.bucket_start <  sqlc.arg(hours_until)::timestamptz
), grouped AS NOT MATERIALIZED (
    SELECT CASE WHEN sqlc.arg(hourly)::bool THEN date_bin('1 hour', bucket_start, 'epoch'::timestamptz)
                ELSE 'epoch'::timestamptz END AS bucket,
           CASE WHEN sqlc.arg(group_by)::text = 'status' THEN status::text ELSE dimension END AS dimension,
           CASE WHEN sqlc.arg(group_by)::text = 'route' THEN method ELSE '' END AS method,
           requests, error_requests, cold_boots, request_time_ms, latency_max_ms, latency_hist, latency_hist_int4
    FROM filtered
    WHERE (sqlc.arg(route)::text = '' OR dimension = sqlc.arg(route)::text)
      AND (sqlc.arg(method)::text = '' OR method = sqlc.arg(method)::text)
), top_groups AS (
    SELECT dimension, method
    FROM grouped
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
       ARRAY[
           SUM(COALESCE(f.latency_hist[1], 0) + COALESCE(f.latency_hist_int4[1], 0)), SUM(COALESCE(f.latency_hist[2], 0) + COALESCE(f.latency_hist_int4[2], 0)), SUM(COALESCE(f.latency_hist[3], 0) + COALESCE(f.latency_hist_int4[3], 0)),
           SUM(COALESCE(f.latency_hist[4], 0) + COALESCE(f.latency_hist_int4[4], 0)), SUM(COALESCE(f.latency_hist[5], 0) + COALESCE(f.latency_hist_int4[5], 0)), SUM(COALESCE(f.latency_hist[6], 0) + COALESCE(f.latency_hist_int4[6], 0)),
           SUM(COALESCE(f.latency_hist[7], 0) + COALESCE(f.latency_hist_int4[7], 0)), SUM(COALESCE(f.latency_hist[8], 0) + COALESCE(f.latency_hist_int4[8], 0)), SUM(COALESCE(f.latency_hist[9], 0) + COALESCE(f.latency_hist_int4[9], 0)),
           SUM(COALESCE(f.latency_hist[10], 0) + COALESCE(f.latency_hist_int4[10], 0)), SUM(COALESCE(f.latency_hist[11], 0) + COALESCE(f.latency_hist_int4[11], 0)), SUM(COALESCE(f.latency_hist[12], 0) + COALESCE(f.latency_hist_int4[12], 0)),
           SUM(COALESCE(f.latency_hist[13], 0) + COALESCE(f.latency_hist_int4[13], 0)), SUM(COALESCE(f.latency_hist[14], 0) + COALESCE(f.latency_hist_int4[14], 0)), SUM(COALESCE(f.latency_hist[15], 0) + COALESCE(f.latency_hist_int4[15], 0)),
           SUM(COALESCE(f.latency_hist[16], 0) + COALESCE(f.latency_hist_int4[16], 0)), SUM(COALESCE(f.latency_hist[17], 0) + COALESCE(f.latency_hist_int4[17], 0)), SUM(COALESCE(f.latency_hist[18], 0) + COALESCE(f.latency_hist_int4[18], 0)),
           SUM(COALESCE(f.latency_hist[19], 0) + COALESCE(f.latency_hist_int4[19], 0)), SUM(COALESCE(f.latency_hist[20], 0) + COALESCE(f.latency_hist_int4[20], 0)), SUM(COALESCE(f.latency_hist[21], 0) + COALESCE(f.latency_hist_int4[21], 0)),
           SUM(COALESCE(f.latency_hist[22], 0) + COALESCE(f.latency_hist_int4[22], 0)), SUM(COALESCE(f.latency_hist[23], 0) + COALESCE(f.latency_hist_int4[23], 0)), SUM(COALESCE(f.latency_hist[24], 0) + COALESCE(f.latency_hist_int4[24], 0)),
           SUM(COALESCE(f.latency_hist[25], 0) + COALESCE(f.latency_hist_int4[25], 0)), SUM(COALESCE(f.latency_hist[26], 0) + COALESCE(f.latency_hist_int4[26], 0)), SUM(COALESCE(f.latency_hist[27], 0) + COALESCE(f.latency_hist_int4[27], 0)),
           SUM(COALESCE(f.latency_hist[28], 0) + COALESCE(f.latency_hist_int4[28], 0)), SUM(COALESCE(f.latency_hist[29], 0) + COALESCE(f.latency_hist_int4[29], 0)), SUM(COALESCE(f.latency_hist[30], 0) + COALESCE(f.latency_hist_int4[30], 0)),
           SUM(COALESCE(f.latency_hist[31], 0) + COALESCE(f.latency_hist_int4[31], 0)), SUM(COALESCE(f.latency_hist[32], 0) + COALESCE(f.latency_hist_int4[32], 0)), SUM(COALESCE(f.latency_hist[33], 0) + COALESCE(f.latency_hist_int4[33], 0)),
           SUM(COALESCE(f.latency_hist[34], 0) + COALESCE(f.latency_hist_int4[34], 0)), SUM(COALESCE(f.latency_hist[35], 0) + COALESCE(f.latency_hist_int4[35], 0)), SUM(COALESCE(f.latency_hist[36], 0) + COALESCE(f.latency_hist_int4[36], 0)),
           SUM(COALESCE(f.latency_hist[37], 0) + COALESCE(f.latency_hist_int4[37], 0)), SUM(COALESCE(f.latency_hist[38], 0) + COALESCE(f.latency_hist_int4[38], 0)), SUM(COALESCE(f.latency_hist[39], 0) + COALESCE(f.latency_hist_int4[39], 0)),
           SUM(COALESCE(f.latency_hist[40], 0) + COALESCE(f.latency_hist_int4[40], 0)), SUM(COALESCE(f.latency_hist[41], 0) + COALESCE(f.latency_hist_int4[41], 0)), SUM(COALESCE(f.latency_hist[42], 0) + COALESCE(f.latency_hist_int4[42], 0)),
           SUM(COALESCE(f.latency_hist[43], 0) + COALESCE(f.latency_hist_int4[43], 0)), SUM(COALESCE(f.latency_hist[44], 0) + COALESCE(f.latency_hist_int4[44], 0)), SUM(COALESCE(f.latency_hist[45], 0) + COALESCE(f.latency_hist_int4[45], 0))
       ]::bigint[] AS latency_hist
FROM grouped AS f
LEFT JOIN top_groups AS t ON t.dimension = f.dimension AND t.method = f.method
GROUP BY f.bucket, t.dimension, t.method
ORDER BY 1 ASC, 4 DESC, 2 ASC, 3 ASC;

-- name: RecordRequestAnalyticsRollupGaps :exec
-- ADR-20261103113952 amendment 1: app minutes whose rollups apid lost.
INSERT INTO request_analytics_rollup_gaps (app_id, bucket_start)
SELECT g.app_id, g.bucket_start
FROM (
    SELECT unnest(sqlc.arg(app_ids)::uuid[]) AS app_id,
           unnest(sqlc.arg(bucket_starts)::timestamptz[]) AS bucket_start
) AS g
ON CONFLICT (app_id, bucket_start) DO NOTHING;

-- name: RequestAnalyticsRollupHasGap :one
-- Whether any minute starting inside the window lost rollups.
SELECT EXISTS (
    SELECT 1 FROM request_analytics_rollup_gaps
    WHERE app_id = sqlc.arg(app_id)
      AND bucket_start >= sqlc.arg(window_from)::timestamptz
      AND bucket_start <  sqlc.arg(window_until)::timestamptz
)::bool AS has_gap;

-- name: RequestAnalyticsRollupRouteDeployments :many
-- Requests per route, method and deployment from the rollups, for the
-- analytics route and deployment cost splits; tiers split as in
-- RequestAnalyticsRollupGroups.
WITH filtered AS NOT MATERIALIZED (
    SELECT dimension, method, deployment_id, requests
    FROM request_analytics_minutes AS rm
    WHERE rm.app_id = sqlc.arg(app_id)
      AND rm.account_id = sqlc.arg(account_id)
      AND rm.dimension_kind = 'route'
      AND ((rm.bucket_start >= sqlc.arg(window_from)::timestamptz AND rm.bucket_start < sqlc.arg(hours_from)::timestamptz)
        OR (rm.bucket_start >= sqlc.arg(hours_until)::timestamptz AND rm.bucket_start < sqlc.arg(window_until)::timestamptz))
    UNION ALL
    SELECT dimension, method, deployment_id, requests
    FROM request_analytics_hours AS rh
    WHERE rh.app_id = sqlc.arg(app_id)
      AND rh.account_id = sqlc.arg(account_id)
      AND rh.dimension_kind = 'route'
      AND rh.bucket_start >= sqlc.arg(hours_from)::timestamptz
      AND rh.bucket_start <  sqlc.arg(hours_until)::timestamptz
)
SELECT dimension::text AS route,
       method::text AS method,
       deployment_id::uuid AS deployment_id,
       SUM(requests)::bigint AS requests
FROM filtered
GROUP BY dimension, method, deployment_id;

-- name: RequestTelemetryExistsBetween :one
-- Whether the app has raw telemetry received in [window_from, window_until).
-- Analytics backdates a rollup tier's coverage over a span with none
-- (ADR-20261103113952 amendment 4).
SELECT EXISTS (
    SELECT 1 FROM request_telemetry
    WHERE app_id = sqlc.arg(app_id)
      AND received_at >= sqlc.arg(window_from)::timestamptz
      AND received_at <  sqlc.arg(window_until)::timestamptz
)::bool AS has_rows;

-- name: RecordRequestAnalyticsRollupGapsFromTelemetry :exec
-- ADR-20261103113952 amendment 4: after apid stopped without writing its
-- pending rollups, mark every app minute with raw telemetry in the span it
-- may have held as lost.
INSERT INTO request_analytics_rollup_gaps (app_id, bucket_start)
SELECT DISTINCT rt.app_id, date_bin('1 minute', rt.received_at, 'epoch'::timestamptz)
FROM request_telemetry rt
JOIN apps a ON a.id = rt.app_id
WHERE rt.received_at >= sqlc.arg(window_from)::timestamptz
  AND rt.received_at <  sqlc.arg(window_until)::timestamptz
ON CONFLICT (app_id, bucket_start) DO NOTHING;

-- name: ClaimRequestAnalyticsRollupBatch :execrows
-- ADR-20261103113952 amendment 5: record a gateway rollup batch in the
-- transaction that applies it; 0 rows means the batch was applied before.
INSERT INTO request_analytics_rollup_batches (batch_id, node_id)
VALUES (sqlc.arg(batch_id), sqlc.arg(node_id))
ON CONFLICT (batch_id) DO NOTHING;
