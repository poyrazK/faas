-- name: RecordTelemetryCoverage :exec
SELECT record_telemetry_coverage(sqlc.arg(node_name)::text, sqlc.arg(boot_id)::uuid, sqlc.arg(sequence)::bigint,
 sqlc.arg(enabled)::boolean, sqlc.arg(sampling_basis_points)::integer, sqlc.arg(dropped_total)::bigint,
 sqlc.arg(pending_count)::integer, sqlc.arg(source_at)::timestamptz,
 sqlc.arg(app_scoped)::boolean,sqlc.arg(unattributed_dropped_total)::bigint,sqlc.arg(app_gaps)::jsonb,sqlc.arg(clean_stop)::boolean);

-- name: LockRouteRemovalCoverage :exec
SELECT lock_route_removal_coverage(sqlc.arg(app_id)::uuid);

-- name: RouteRemovalCoverageBlockers :one
SELECT route_removal_coverage_blockers(sqlc.arg(app_id)::uuid,sqlc.arg(window_from)::timestamptz, sqlc.arg(window_until)::timestamptz)::jsonb;

-- name: RecordRequestAnalyticsCoverage :exec
SELECT record_request_analytics_coverage(sqlc.arg(node_name)::text, sqlc.arg(boot_id)::uuid, sqlc.arg(counting)::boolean,
 sqlc.arg(unattributed_dropped_total)::bigint, sqlc.arg(app_gaps)::jsonb);

-- name: RequestAnalyticsCompletenessBlockers :one
SELECT request_analytics_completeness_blockers(sqlc.arg(app_id)::uuid,sqlc.arg(window_from)::timestamptz, sqlc.arg(window_until)::timestamptz)::jsonb;

-- name: RequestTelemetryCoveredSince :one
-- The latest start of continuous healthy coverage across the active gateways
-- that reported at or after reported_since, and whether every one of them is
-- fully recording now. A release guard whose window a gateway restart split
-- judges the part after it instead of holding for good.
SELECT count(*)::bigint AS nodes,
 coalesce(bool_and(c.enabled AND c.sampling_basis_points = 10000 AND c.healthy_since IS NOT NULL), false)::boolean AS healthy,
 max(c.healthy_since)::timestamptz AS covered_since
FROM request_telemetry_coverage c
JOIN compute_nodes n ON n.name = c.node_name AND n.active
WHERE c.source_at >= sqlc.arg(reported_since)::timestamptz;
