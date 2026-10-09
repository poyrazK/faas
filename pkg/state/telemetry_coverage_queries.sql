-- name: RecordTelemetryCoverage :exec
SELECT record_telemetry_coverage(sqlc.arg(node_name)::text, sqlc.arg(boot_id)::uuid, sqlc.arg(sequence)::bigint,
 sqlc.arg(enabled)::boolean, sqlc.arg(sampling_basis_points)::integer, sqlc.arg(dropped_total)::bigint,
 sqlc.arg(pending_count)::integer, sqlc.arg(source_at)::timestamptz,
 sqlc.arg(app_scoped)::boolean,sqlc.arg(unattributed_dropped_total)::bigint,sqlc.arg(app_gaps)::jsonb);

-- name: LockRouteRemovalCoverage :exec
SELECT lock_route_removal_coverage(sqlc.arg(app_id)::uuid);

-- name: RouteRemovalCoverageBlockers :one
SELECT route_removal_coverage_blockers(sqlc.arg(app_id)::uuid,sqlc.arg(window_from)::timestamptz, sqlc.arg(window_until)::timestamptz)::jsonb;
