-- Route advisor reads (ADR-976). Retained debugger telemetry only; nothing
-- here touches the usage ledger. Route labels are "METHOD /template".

-- name: RouteAdviceRouteStats :many
-- The busiest routes of an app across deployments. The cache estimate buckets
-- anonymous 2xx GET/HEAD requests into cache-lifetime windows: the first
-- request of a window fills the cache, the rest are hits, and a cold boot
-- that is not first in its window is a wake the cache would have avoided.
-- The content_* counts are learned cacheability (ADR-1188): how observed
-- anonymous GET bodies compared with the previous response for their URL,
-- bucketed by api.RouteContentWindows (60 s, 300 s, 3600 s). content_stale
-- counts refills under a cache rule whose body changed within the rule's
-- max-age (ADR-1199).
WITH filtered AS MATERIALIZED (
    SELECT rt.route, rt.method, rt.status, rt.latency_ms, rt.cold_boot, rt.guest_outcome,
           rt.content_outcome, rt.count::bigint AS requests, rt.received_at,
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
           COALESCE(percentile_disc(0.95) WITHIN GROUP (ORDER BY latency_ms), 0)::int AS p95_latency_ms,
           COALESCE(SUM(requests) FILTER (WHERE content_outcome = 'first'), 0)::bigint AS content_first,
           COALESCE(SUM(requests) FILTER (WHERE content_outcome = 'uncacheable'), 0)::bigint AS content_uncacheable,
           COALESCE(SUM(requests) FILTER (WHERE content_outcome = 'same_60'), 0)::bigint AS content_same_60,
           COALESCE(SUM(requests) FILTER (WHERE content_outcome = 'same_300'), 0)::bigint AS content_same_300,
           COALESCE(SUM(requests) FILTER (WHERE content_outcome = 'same_3600'), 0)::bigint AS content_same_3600,
           COALESCE(SUM(requests) FILTER (WHERE content_outcome = 'changed_60'), 0)::bigint AS content_changed_60,
           COALESCE(SUM(requests) FILTER (WHERE content_outcome = 'changed_300'), 0)::bigint AS content_changed_300,
           COALESCE(SUM(requests) FILTER (WHERE content_outcome = 'changed_3600'), 0)::bigint AS content_changed_3600,
           COALESCE(SUM(requests) FILTER (WHERE content_outcome = 'stale'), 0)::bigint AS content_stale
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
       t.content_first, t.content_uncacheable,
       t.content_same_60, t.content_same_300, t.content_same_3600,
       t.content_changed_60, t.content_changed_300, t.content_changed_3600, t.content_stale,
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
