-- name: RouteMonitorIncidentsSince :many
-- ADR-20261028110044: an app's production route monitor incidents open at any
-- time at or after since (still open, or closed at or after since), newest
-- first, for the route_monitor_incidents alert metric. total counts every
-- match, not only the rows returned.
SELECT id::text AS id, status, opened_at,
       COALESCE(entry #>> '{opening_report,reason}', '')::text AS reason,
       COALESCE(entry #>> '{evidence,0,method}', '')::text AS method,
       COALESCE(entry #>> '{evidence,0,path}', '')::text AS path,
       COALESCE(entry #>> '{evidence,0,signal}', '')::text AS signal,
       count(*) OVER ()::bigint AS total
FROM route_monitor_incidents
WHERE app_id = sqlc.arg(app_id)::text::uuid
  AND (status = 'open' OR closed_at >= sqlc.arg(since))
ORDER BY opened_at DESC, id DESC
LIMIT sqlc.arg(row_limit)::int;

-- name: RouteHealthHoldsSince :many
-- ADR-20261028110044: an app's in-flight canaries the enforce-mode route
-- health guard holds now, and its canaries the guard aborted at or after
-- since, newest first, for the route_health_holds alert metric. reason is the
-- newest holding decision's. total counts every match.
SELECT s.deployment_id::text AS deployment_id, s.status, s.updated_at,
       COALESCE(h.entry #>> '{decision,reason}', '')::text AS reason,
       count(*) OVER ()::bigint AS total
FROM deployments d
JOIN route_health_notification_state s ON s.deployment_id = d.id AND s.app_id = d.app_id
LEFT JOIN LATERAL (
  SELECT entry FROM route_health_history
  WHERE deployment_id = s.deployment_id AND entry #>> '{decision,status}' IN ('blocked', 'aborted')
  ORDER BY checked_at DESC, id DESC
  LIMIT 1
) h ON true
WHERE d.app_id = sqlc.arg(app_id)::text::uuid
  AND ((s.status IN ('blocked_unknown', 'blocked_regressed') AND d.status = 'live' AND d.deleted_at IS NULL
        AND d.canary_total_steps > 0 AND d.canary_step < d.canary_total_steps)
    OR (s.status = 'aborted' AND s.updated_at >= sqlc.arg(since)))
ORDER BY s.updated_at DESC, s.deployment_id
LIMIT sqlc.arg(row_limit)::int;

-- name: ObservedRouteLabels :many
-- ADR-20261028110044: which of the given route labels ("METHOD /path") the
-- app served at or after since, so saving a route guard or monitor can warn
-- about selectors no request matched.
SELECT DISTINCT route::text AS route
FROM request_telemetry
WHERE app_id = sqlc.arg(app_id)::text::uuid
  AND account_id = sqlc.arg(account_id)::text::uuid
  AND received_at >= sqlc.arg(since)
  AND route = ANY(sqlc.arg(labels)::text[]);
