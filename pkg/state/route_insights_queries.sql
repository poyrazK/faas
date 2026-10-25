-- Route insights (ADR-1198): findings apid's evaluator derives from the
-- awake report and learned cacheability. A trigger on route_insights enqueues
-- the customer webhooks for each detected/resolved transition.

-- name: RouteInsightCandidateApps :many
-- Apps on the given plans that ran in the window and that no evaluation has
-- taken since due_before, least recently evaluated first, with their
-- account (ADR-20261024154324). An app that stayed parked has nothing to
-- report.
SELECT a.id AS app_id, a.account_id
FROM apps a
JOIN accounts acc ON acc.id = a.account_id
LEFT JOIN route_insight_evaluations e ON e.app_id = a.id
WHERE a.id IN (
        SELECT um.app_id
        FROM usage_minutes um
        WHERE um.meter_kind = 'app'
          AND um.minute >= sqlc.arg(since_at)
          AND um.mb_seconds > 0
    )
  AND a.status <> 'deleted'
  AND acc.plan = ANY(sqlc.arg(plans)::text[])
  AND (e.evaluated_at IS NULL OR e.evaluated_at < sqlc.arg(due_before))
ORDER BY e.evaluated_at NULLS FIRST, a.id
LIMIT sqlc.arg(max_apps)::int;

-- name: ClaimRouteInsightEvaluation :execrows
-- Take an app for this evaluation unless another evaluation took it since
-- due_before; one affected row means this caller evaluates it
-- (ADR-20261024154324).
INSERT INTO route_insight_evaluations (app_id, account_id, evaluated_at)
VALUES (sqlc.arg(app_id), sqlc.arg(account_id), sqlc.arg(evaluated_at))
ON CONFLICT (app_id) DO UPDATE SET account_id = EXCLUDED.account_id, evaluated_at = EXCLUDED.evaluated_at
WHERE route_insight_evaluations.evaluated_at < sqlc.arg(due_before);

-- name: UpsertRouteInsight :exec
-- Record an observed insight. A new or resolved insight (re)opens with this
-- observation as its first detection; an active one only refreshes. An
-- insight a fix resolved after refix_before stays resolved: its evidence
-- window still holds observations from before the fix
-- (ADR-20261020161310).
INSERT INTO route_insights (account_id, app_id, kind, method, route, state, message, evidence, first_detected_at, last_detected_at)
VALUES (sqlc.arg(account_id), sqlc.arg(app_id), sqlc.arg(kind), sqlc.arg(method), sqlc.arg(route), 'active',
        sqlc.arg(message), sqlc.arg(evidence), sqlc.arg(observed_at), sqlc.arg(observed_at))
ON CONFLICT (app_id, kind, method, route) DO UPDATE SET
    state = 'active',
    message = EXCLUDED.message,
    evidence = EXCLUDED.evidence,
    first_detected_at = CASE WHEN route_insights.state = 'resolved' THEN EXCLUDED.first_detected_at ELSE route_insights.first_detected_at END,
    last_detected_at = EXCLUDED.last_detected_at,
    resolved_at = NULL,
    fixed_at = NULL,
    fix_action = NULL
WHERE route_insights.fixed_at IS NULL OR route_insights.fixed_at < sqlc.arg(refix_before);

-- name: ResolveStaleRouteInsights :execrows
-- Resolve an app's active insights that no evaluation has observed since
-- stale_before.
UPDATE route_insights
SET state = 'resolved', resolved_at = sqlc.arg(resolved_at)
WHERE app_id = sqlc.arg(app_id)
  AND state = 'active'
  AND last_detected_at < sqlc.arg(stale_before);

-- name: ResolveStaleRouteInsightsBatch :execrows
-- Resolve up to row_limit active insights of any app that no evaluation has
-- observed since stale_before: an app that stopped running or left a plan
-- with request telemetry is no longer evaluated (ADR-20261024154324).
UPDATE route_insights
SET state = 'resolved', resolved_at = sqlc.arg(resolved_at)
WHERE id IN (
    SELECT stale.id
    FROM route_insights stale
    WHERE stale.state = 'active'
      AND stale.last_detected_at < sqlc.arg(stale_before)
    ORDER BY stale.last_detected_at
    LIMIT sqlc.arg(row_limit)::int
    FOR UPDATE SKIP LOCKED
);

-- name: ListRouteInsights :many
-- An app's active insights, then the ones resolved since resolved_since.
SELECT id, kind, method, route, state, message, evidence, first_detected_at, last_detected_at, resolved_at, fixed_at, fix_action
FROM route_insights
WHERE app_id = sqlc.arg(app_id)
  AND account_id = sqlc.arg(account_id)
  AND (state = 'active' OR resolved_at >= sqlc.arg(resolved_since))
ORDER BY (state = 'active') DESC, last_detected_at DESC, kind, method, route
LIMIT sqlc.arg(row_limit)::int;

-- name: GetRouteInsight :one
-- One insight of an app, for applying its fix.
SELECT id, kind, method, route, state, message, evidence, first_detected_at, last_detected_at, resolved_at, fixed_at, fix_action
FROM route_insights
WHERE id = sqlc.arg(id)
  AND app_id = sqlc.arg(app_id)
  AND account_id = sqlc.arg(account_id);

-- name: MarkRouteInsightsFixed :execrows
-- Resolve a route's active insights of the given kinds with the fix that
-- resolved them (ADR-20261020161310). The webhook trigger reports each as
-- resolved.
UPDATE route_insights
SET state = 'resolved', resolved_at = sqlc.arg(fixed_at), fixed_at = sqlc.arg(fixed_at), fix_action = sqlc.arg(fix_action)
WHERE app_id = sqlc.arg(app_id)
  AND account_id = sqlc.arg(account_id)
  AND method = sqlc.arg(method)
  AND route = sqlc.arg(route)
  AND kind = ANY(sqlc.arg(kinds)::text[])
  AND state = 'active';

-- name: RouteInsightsDetectedSince :many
-- An app's active insights that became active at or after since, newest
-- first, for the route_insights_detected alert metric. total counts every
-- match, not only the rows returned.
SELECT kind, method, route, message, first_detected_at, count(*) OVER ()::bigint AS total
FROM route_insights
WHERE app_id = sqlc.arg(app_id)
  AND state = 'active'
  AND first_detected_at >= sqlc.arg(since)
ORDER BY first_detected_at DESC, kind, method, route
LIMIT sqlc.arg(row_limit)::int;
