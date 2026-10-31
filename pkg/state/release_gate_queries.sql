-- ADR-20261019071734: release gates on customer signals.

-- name: CreateAppReleaseGateWithinLimit :one
-- Inserts a gate only while the app holds fewer than max_gates gates.
INSERT INTO app_release_gates (
  id, account_id, app_id, name, kind, slo_id, max_burn_rate, max_new_groups, min_events,
  metric_name, comparison, threshold, min_samples, created_by, created_at
)
SELECT sqlc.arg(id)::uuid, sqlc.arg(account_id)::uuid, sqlc.arg(app_id)::uuid, sqlc.arg(name)::text,
  sqlc.arg(kind)::text, sqlc.narg(slo_id)::uuid, sqlc.narg(max_burn_rate)::integer,
  sqlc.narg(max_new_groups)::integer, sqlc.narg(min_events)::integer, sqlc.narg(metric_name)::text,
  sqlc.narg(comparison)::text, sqlc.narg(threshold)::double precision, sqlc.arg(min_samples)::integer,
  sqlc.arg(created_by)::text, sqlc.arg(now)::timestamptz
WHERE (SELECT count(*) FROM app_release_gates g WHERE g.app_id = sqlc.arg(app_id)::uuid) < sqlc.arg(max_gates)::bigint
RETURNING *;

-- name: ListAppReleaseGates :many
SELECT * FROM app_release_gates WHERE app_id = sqlc.arg(app_id)::uuid ORDER BY name;

-- name: DeleteAppReleaseGate :one
DELETE FROM app_release_gates
 WHERE app_id = sqlc.arg(app_id)::uuid AND name = sqlc.arg(name)::text
RETURNING *;

-- name: AppSLOForReleaseGate :one
SELECT id, sli, latency_threshold_ms, objective_bp FROM app_slos
 WHERE id = sqlc.arg(id)::uuid AND app_id = sqlc.arg(app_id)::uuid;

-- name: ReleaseGateDeploymentSLI :one
-- A deployment's organic requests in [since, until): all, 5xx, and those
-- slower than latency_threshold_ms (0 counts none as slow).
SELECT COALESCE(SUM(count::bigint), 0)::bigint AS requests,
       COALESCE(SUM(count::bigint) FILTER (WHERE status >= 500 AND status < 600), 0)::bigint AS server_errors,
       COALESCE(SUM(count::bigint) FILTER (WHERE sqlc.arg(latency_threshold_ms)::integer > 0
         AND latency_ms > sqlc.arg(latency_threshold_ms)::integer), 0)::bigint AS slow_requests
  FROM request_telemetry
 WHERE app_id = sqlc.arg(app_id)::uuid
   AND deployment_id = sqlc.arg(deployment_id)::uuid
   AND received_at >= sqlc.arg(since)::timestamptz
   AND received_at < sqlc.arg(until)::timestamptz;

-- name: ReleaseGateNewErrorGroups :many
-- Error groups the deployment produced first, with at least min_events
-- events, largest first. Error classes are a closed set; routes and
-- messages are not returned.
SELECT error_class, count::bigint AS events
  FROM app_errors
 WHERE app_id = sqlc.arg(app_id)::uuid
   AND deployment_id = sqlc.arg(deployment_id)::uuid
   AND count >= sqlc.arg(min_events)::bigint
 ORDER BY count DESC, fingerprint
 LIMIT 101;

-- name: ReleaseGateCustomMetricWindow :one
SELECT COUNT(*)::bigint AS samples, COALESCE(AVG(value), 0)::double precision AS average
  FROM app_custom_metrics
 WHERE app_id = sqlc.arg(app_id)::uuid
   AND name = sqlc.arg(name)::text
   AND observed_at >= sqlc.arg(since)::timestamptz
   AND observed_at < sqlc.arg(until)::timestamptz;

-- name: ReleaseGateDeploymentAI :one
-- ADR 20261022153155: a deployment's AI calls in the UTC minutes of
-- [since, until).
SELECT COALESCE(SUM(calls), 0)::bigint AS calls,
       COALESCE(SUM(error_calls), 0)::bigint AS error_calls,
       COALESCE(SUM(flagged_calls), 0)::bigint AS flagged_calls,
       COALESCE(SUM(cost_millicents), 0)::bigint AS cost_millicents,
       COALESCE(SUM(ttfb_calls), 0)::bigint AS ttfb_calls,
       COALESCE(SUM(ttfb_ms_total), 0)::bigint AS ttfb_ms_total
  FROM outbound_ai_deployment_minutes
 WHERE deployment_id = sqlc.arg(deployment_id)::uuid
   AND app_id = sqlc.arg(app_id)::uuid
   AND minute >= date_trunc('minute', sqlc.arg(since)::timestamptz)
   AND minute < sqlc.arg(until)::timestamptz;
