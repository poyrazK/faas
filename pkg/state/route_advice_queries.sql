-- Route advisor reads (ADR-955). Retained debugger telemetry only; nothing
-- here touches the usage ledger. Route labels are "METHOD /template".

-- name: RouteAdviceRouteStats :many
-- The busiest routes of an app across deployments. The cache estimate buckets
-- anonymous 2xx GET/HEAD requests into cache-lifetime windows: the first
-- request of a window fills the cache, the rest are hits, and a cold boot
-- that is not first in its window is a wake the cache would have avoided.
WITH filtered AS MATERIALIZED (
    SELECT rt.route, rt.method, rt.status, rt.latency_ms, rt.cold_boot, rt.guest_outcome,
           rt.count::bigint AS requests, rt.received_at,
           (rt.consumer_id IS NULL AND rt.platform_tenant_id IS NULL) AS anonymous
    FROM request_telemetry rt
    WHERE rt.app_id = sqlc.arg(app_id)
      AND rt.account_id = sqlc.arg(account_id)
      AND rt.received_at >= sqlc.arg(since_at)
      AND rt.received_at < sqlc.arg(until_at)
      AND starts_with(rt.route, rt.method || ' /')
), totals AS (
    SELECT route, method,
           SUM(requests)::bigint AS requests,
           COALESCE(SUM(requests) FILTER (WHERE anonymous), 0)::bigint AS anonymous_requests,
           COALESCE(SUM(requests) FILTER (WHERE anonymous AND status BETWEEN 200 AND 299), 0)::bigint AS anonymous_success,
           COALESCE(SUM(requests) FILTER (WHERE status >= 500), 0)::bigint AS server_errors,
           COALESCE(SUM(requests) FILTER (WHERE guest_outcome = 'timeout' OR status = 504), 0)::bigint AS timeouts,
           COALESCE(SUM(requests) FILTER (WHERE cold_boot), 0)::bigint AS cold_boots,
           COALESCE(percentile_disc(0.95) WITHIN GROUP (ORDER BY latency_ms), 0)::int AS p95_latency_ms
    FROM filtered GROUP BY route, method
), top_routes AS (
    SELECT * FROM totals
    ORDER BY requests DESC, route ASC, method ASC
    LIMIT sqlc.arg(route_limit)::int
), cacheable AS (
    SELECT f.route, f.method, f.requests, f.cold_boot,
           ROW_NUMBER() OVER (
               PARTITION BY f.route, f.method, floor(extract(epoch FROM f.received_at) / sqlc.arg(cache_max_age_seconds)::int)
               ORDER BY f.received_at, f.cold_boot
           ) AS position
    FROM filtered f JOIN top_routes USING (route, method)
    WHERE f.anonymous AND f.status BETWEEN 200 AND 299 AND f.method IN ('GET', 'HEAD')
), cache_estimate AS (
    SELECT route, method,
           SUM(CASE WHEN position = 1 THEN requests - 1 ELSE requests END)::bigint AS cache_hits,
           COALESCE(SUM(requests) FILTER (WHERE cold_boot AND position > 1), 0)::bigint AS wakes_avoided
    FROM cacheable GROUP BY route, method
)
SELECT t.route, t.method, t.requests, t.anonymous_requests, t.anonymous_success,
       t.server_errors, t.timeouts, t.cold_boots, t.p95_latency_ms,
       COALESCE(c.cache_hits, 0)::bigint AS cache_hits,
       COALESCE(c.wakes_avoided, 0)::bigint AS wakes_avoided
FROM top_routes t LEFT JOIN cache_estimate c USING (route, method)
ORDER BY t.requests DESC, t.route ASC, t.method ASC;

-- name: RouteAdviceConsumers :many
-- The two busiest identified consumers of each route with their peak minute.
-- The consumer join validates ownership; it never infers one from today's link.
WITH per_minute AS (
    SELECT rt.route, rt.method, c.id AS consumer_id,
           date_trunc('minute', rt.received_at) AS minute, SUM(rt.count)::bigint AS requests
    FROM request_telemetry rt
    JOIN api_consumers c ON c.id = rt.consumer_id AND c.account_id = rt.account_id AND c.app_id = rt.app_id
    WHERE rt.app_id = sqlc.arg(app_id)
      AND rt.account_id = sqlc.arg(account_id)
      AND rt.received_at >= sqlc.arg(since_at)
      AND rt.received_at < sqlc.arg(until_at)
      AND starts_with(rt.route, rt.method || ' /')
    GROUP BY rt.route, rt.method, c.id, date_trunc('minute', rt.received_at)
), per_consumer AS (
    SELECT route, method, consumer_id,
           SUM(requests)::bigint AS requests, MAX(requests)::bigint AS peak_per_minute
    FROM per_minute GROUP BY route, method, consumer_id
), ranked AS (
    SELECT per_consumer.*,
           ROW_NUMBER() OVER (PARTITION BY route, method ORDER BY requests DESC, consumer_id ASC) AS consumer_rank,
           COUNT(*) OVER (PARTITION BY route, method)::bigint AS consumers
    FROM per_consumer
)
SELECT route, method, consumer_id, requests, peak_per_minute, consumer_rank::int AS consumer_rank, consumers
FROM ranked
WHERE consumer_rank <= 2
ORDER BY route ASC, method ASC, consumer_rank ASC;

-- name: RouteAdviceThrottleExcess :one
-- Requests one consumer sent above a per-minute allowance on one route.
SELECT COALESCE(SUM(GREATEST(per_minute.requests - sqlc.arg(allowance_per_minute)::bigint, 0)), 0)::bigint AS excess
FROM (
    SELECT SUM(rt.count)::bigint AS requests
    FROM request_telemetry rt
    WHERE rt.app_id = sqlc.arg(app_id)
      AND rt.account_id = sqlc.arg(account_id)
      AND rt.consumer_id = sqlc.arg(consumer_id)
      AND rt.route = sqlc.arg(route)::text
      AND rt.method = sqlc.arg(method)::text
      AND rt.received_at >= sqlc.arg(since_at)
      AND rt.received_at < sqlc.arg(until_at)
    GROUP BY date_trunc('minute', rt.received_at)
) per_minute;
