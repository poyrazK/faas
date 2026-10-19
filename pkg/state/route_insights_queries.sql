-- Route insights (ADR-1198): findings apid's evaluator derives from the
-- awake report and learned cacheability. A trigger on route_insights enqueues
-- the customer webhooks for each detected/resolved transition.

-- name: RouteInsightCandidateApps :many
-- Apps that ran in the window, with their account; an app that stayed parked
-- has nothing to report.
SELECT DISTINCT um.app_id::uuid AS app_id, um.account_id
FROM usage_minutes um
WHERE um.meter_kind = 'app'
  AND um.minute >= sqlc.arg(since_at)
  AND um.mb_seconds > 0
ORDER BY 1
LIMIT sqlc.arg(max_apps)::int;

-- name: UpsertRouteInsight :exec
-- Record an observed insight. A new or resolved insight (re)opens with this
-- observation as its first detection; an active one only refreshes.
INSERT INTO route_insights (account_id, app_id, kind, method, route, state, message, evidence, first_detected_at, last_detected_at)
VALUES (sqlc.arg(account_id), sqlc.arg(app_id), sqlc.arg(kind), sqlc.arg(method), sqlc.arg(route), 'active',
        sqlc.arg(message), sqlc.arg(evidence), sqlc.arg(observed_at), sqlc.arg(observed_at))
ON CONFLICT (app_id, kind, method, route) DO UPDATE SET
    state = 'active',
    message = EXCLUDED.message,
    evidence = EXCLUDED.evidence,
    first_detected_at = CASE WHEN route_insights.state = 'resolved' THEN EXCLUDED.first_detected_at ELSE route_insights.first_detected_at END,
    last_detected_at = EXCLUDED.last_detected_at,
    resolved_at = NULL;

-- name: ResolveStaleRouteInsights :execrows
-- Resolve an app's active insights that no evaluation has observed since
-- stale_before.
UPDATE route_insights
SET state = 'resolved', resolved_at = sqlc.arg(resolved_at)
WHERE app_id = sqlc.arg(app_id)
  AND state = 'active'
  AND last_detected_at < sqlc.arg(stale_before);

-- name: ListRouteInsights :many
-- An app's active insights, then the ones resolved since resolved_since.
SELECT kind, method, route, state, message, evidence, first_detected_at, last_detected_at, resolved_at
FROM route_insights
WHERE app_id = sqlc.arg(app_id)
  AND account_id = sqlc.arg(account_id)
  AND (state = 'active' OR resolved_at >= sqlc.arg(resolved_since))
ORDER BY (state = 'active') DESC, last_detected_at DESC, kind, method, route
LIMIT sqlc.arg(row_limit)::int;
