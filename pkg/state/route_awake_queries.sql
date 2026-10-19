-- Route awake report reads (ADR-1187): which routes keep a scale-to-zero app
-- awake. usage_minutes is the billed running time; request_telemetry is
-- retained debugger telemetry. Both are read only.

-- name: RouteAwakeMinutes :many
-- The app's billed running time and cold boots per minute, summed over its
-- instances.
SELECT um.minute::timestamptz AS minute,
       SUM(um.mb_seconds)::bigint AS mb_seconds,
       SUM(um.cold_boot_count)::bigint AS cold_boots
FROM usage_minutes um
WHERE um.account_id = sqlc.arg(account_id)
  AND um.app_id = sqlc.arg(app_id)
  AND um.meter_kind = 'app'
  AND um.minute >= sqlc.arg(since_at)
  AND um.minute < sqlc.arg(until_at)
GROUP BY um.minute
HAVING SUM(um.mb_seconds) > 0
ORDER BY um.minute;

-- name: RouteAwakeRouteMinutes :many
-- Requests that restart an instance's idle timer, per minute and route, for
-- the busiest routes; the rest fold into the empty route. Only a 2xx response
-- from an instance restarts the timer, and a cold-boot request starts it
-- whatever it returns. Requests the gateway answered itself (cache hits, edge
-- answers, auth rejections) carry no deployment, instance or wake.
WITH filtered AS MATERIALIZED (
    SELECT rt.route, rt.method, date_trunc('minute', rt.received_at) AS minute,
           rt.count::bigint AS requests, rt.cold_boot,
           (rt.consumer_id IS NULL AND rt.platform_tenant_id IS NULL) AS anonymous
    FROM request_telemetry rt
    WHERE rt.app_id = sqlc.arg(app_id)
      AND rt.account_id = sqlc.arg(account_id)
      AND rt.received_at >= sqlc.arg(since_at)
      AND rt.received_at < sqlc.arg(until_at)
      AND (rt.status BETWEEN 200 AND 299 OR rt.cold_boot)
      AND (rt.deployment_id <> '00000000-0000-0000-0000-000000000000'::uuid
           OR COALESCE(rt.instance_id, '') <> ''
           OR rt.wake_id IS NOT NULL)
      AND starts_with(rt.route, rt.method || ' /')
), top_routes AS (
    SELECT route, method FROM filtered
    GROUP BY route, method
    ORDER BY SUM(requests) DESC, route ASC, method ASC
    LIMIT sqlc.arg(route_limit)::int
)
SELECT f.minute::timestamptz AS minute,
       COALESCE(t.route, '')::text AS route,
       COALESCE(t.method, '')::text AS method,
       SUM(f.requests)::bigint AS requests,
       COALESCE(SUM(f.requests) FILTER (WHERE f.cold_boot), 0)::bigint AS cold_boots,
       COALESCE(SUM(f.requests) FILTER (WHERE f.anonymous), 0)::bigint AS anonymous_requests
FROM filtered f
LEFT JOIN top_routes t ON t.route = f.route AND t.method = f.method
GROUP BY f.minute, COALESCE(t.route, ''), COALESCE(t.method, '')
ORDER BY f.minute, COALESCE(t.route, ''), COALESCE(t.method, '');

-- name: RouteAwakeSourceMinutes :many
-- Wakes and keep-alives that are not customer requests (ADR-1199), per minute
-- and source: synthetic route probes the app answered without a 5xx or an
-- auth rejection (ADR-847; probe observations are pruned after a day), and
-- scheduled prewarms that admitted instances.
SELECT s.minute::timestamptz AS minute, s.source::text AS source, SUM(s.events)::bigint AS events
FROM (
    SELECT date_trunc('minute', o.window_start) AS minute, 'route_probe' AS source,
           (o.requests - o.server_errors - o.unauthenticated)::bigint AS events
    FROM route_probe_observations o
    WHERE o.app_id = sqlc.arg(app_id)
      AND o.account_id = sqlc.arg(account_id)
      AND o.window_start >= sqlc.arg(since_at)
      AND o.window_start < sqlc.arg(until_at)
    UNION ALL
    SELECT date_trunc('minute', p.fired_at) AS minute, 'prewarm' AS source, p.admitted_count::bigint AS events
    FROM prewarm_intents p
    WHERE p.app_id = sqlc.arg(app_id)
      AND p.account_id = sqlc.arg(account_id)
      AND p.fired_at >= sqlc.arg(since_at)
      AND p.fired_at < sqlc.arg(until_at)
      AND p.admitted_count > 0
) s
WHERE s.events > 0
GROUP BY s.minute, s.source
ORDER BY s.minute, s.source;
