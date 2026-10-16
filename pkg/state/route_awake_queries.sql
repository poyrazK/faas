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
      AND (sqlc.arg(selected_scope)::text = '' OR EXISTS (
          SELECT 1 FROM instances i JOIN deployments d ON d.id=i.deployment_id JOIN apps a ON a.id=d.app_id
          WHERE i.id=um.instance_id AND d.app_id=um.app_id AND a.account_id=um.account_id AND i.app_id=um.app_id
          AND d.scope=sqlc.arg(selected_scope)::text
          AND (d.scope='default' OR EXISTS (
              SELECT 1 FROM project_environment_workload_deployment_specs ds
              JOIN project_environment_workload_specs spec ON spec.id=ds.spec_id
              JOIN project_environments e ON e.id=spec.environment_id
              WHERE ds.deployment_id=d.id AND spec.app_id=a.id
              AND e.id::text=sqlc.arg(selected_environment_id)::text
              AND e.project_id=a.project_id AND e.account_id=a.account_id AND e.slug=d.scope
          ))
      ))
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
      AND (sqlc.arg(selected_scope)::text = '' OR EXISTS (
          SELECT 1 FROM deployments d JOIN apps a ON a.id=d.app_id
          WHERE d.id=rt.deployment_id AND d.app_id=rt.app_id AND a.account_id=rt.account_id
          AND d.scope=sqlc.arg(selected_scope)::text
          AND (d.scope='default' OR EXISTS (
              SELECT 1 FROM project_environment_workload_deployment_specs ds
              JOIN project_environment_workload_specs spec ON spec.id=ds.spec_id
              JOIN project_environments e ON e.id=spec.environment_id
              WHERE ds.deployment_id=d.id AND spec.app_id=a.id
              AND e.id::text=sqlc.arg(selected_environment_id)::text
              AND e.project_id=a.project_id AND e.account_id=a.account_id AND e.slug=d.scope
          ))
      ))
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
