-- ADR-20261019071734: release gates on customer signals.

-- name: CreateAppReleaseGateWithinLimit :one
-- Inserts a gate only while the app holds fewer than max_gates gates.
INSERT INTO app_release_gates (
  id, account_id, app_id, name, kind, slo_id, max_burn_rate, max_new_groups, min_events,
  metric_name, comparison, threshold, min_samples, created_by, created_at,
  synthetic_check_id, max_failure_percent, max_change_percent
)
SELECT sqlc.arg(id)::uuid, sqlc.arg(account_id)::uuid, sqlc.arg(app_id)::uuid, sqlc.arg(name)::text,
  sqlc.arg(kind)::text, sqlc.narg(slo_id)::uuid, sqlc.narg(max_burn_rate)::integer,
  sqlc.narg(max_new_groups)::integer, sqlc.narg(min_events)::integer, sqlc.narg(metric_name)::text,
  sqlc.narg(comparison)::text, sqlc.narg(threshold)::double precision, sqlc.arg(min_samples)::integer,
  sqlc.arg(created_by)::text, sqlc.arg(now)::timestamptz,
  sqlc.narg(synthetic_check_id)::uuid, sqlc.narg(max_failure_percent)::integer, sqlc.narg(max_change_percent)::integer
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

-- ADR-20261022082305: synthetic gates probe canaries.

-- name: ListSyntheticGateProbeTargets :many
-- Each synthetic gate (with an enabled check) of a request app whose
-- default-scope canary is rolling out beside a serving predecessor. An app
-- with several serving predecessors returns several rows per gate and is
-- skipped by the caller.
SELECT a.id::text AS app_id, a.account_id::text AS account_id, a.slug::text AS slug,
 d.id::text AS candidate_id, s.id::text AS stable_id, g.id::text AS gate_id,
 c.method::text AS method, c.path::text AS path, c.expected_status, c.timeout_ms::integer AS timeout_ms
FROM app_release_gates g
JOIN synthetic_checks c ON c.id = g.synthetic_check_id AND c.app_id = g.app_id AND c.enabled
JOIN apps a ON a.id = g.app_id
JOIN deployments d ON d.app_id = a.id AND d.status = 'live' AND d.deleted_at IS NULL AND d.traffic_percent > 0
 AND d.canary_total_steps > 0 AND d.canary_step < d.canary_total_steps
 AND coalesce(nullif(d.scope, ''), 'default') = 'default'
JOIN deployments s ON s.app_id = a.id AND s.id <> d.id AND s.status = 'live' AND s.deleted_at IS NULL
 AND s.traffic_percent > 0 AND coalesce(nullif(s.scope, ''), 'default') = 'default'
WHERE g.kind = 'synthetic' AND a.status <> 'deleted' AND a.preview_of_slug IS NULL
ORDER BY a.id, d.id, s.id, g.id
LIMIT sqlc.arg(batch_limit)::int;

-- name: RecordReleaseGateProbeResult :exec
-- Adds one round's probe counts for a gate and revision.
INSERT INTO release_gate_probe_results (gate_id, app_id, deployment_id, window_start, requests, failures)
VALUES (sqlc.arg(gate_id)::uuid, sqlc.arg(app_id)::uuid, sqlc.arg(deployment_id)::uuid,
  sqlc.arg(window_start)::timestamptz, sqlc.arg(requests)::integer, sqlc.arg(failures)::integer)
ON CONFLICT (gate_id, deployment_id, window_start) DO UPDATE SET
  requests = least(release_gate_probe_results.requests + EXCLUDED.requests, 10000),
  failures = least(release_gate_probe_results.failures + EXCLUDED.failures,
    least(release_gate_probe_results.requests + EXCLUDED.requests, 10000));

-- name: ReleaseGateProbeTotals :one
SELECT coalesce(sum(requests), 0)::bigint AS requests, coalesce(sum(failures), 0)::bigint AS failures
  FROM release_gate_probe_results
 WHERE gate_id = sqlc.arg(gate_id)::uuid
   AND deployment_id = sqlc.arg(deployment_id)::uuid
   AND window_start >= sqlc.arg(since)::timestamptz
   AND window_start < sqlc.arg(until)::timestamptz;

-- name: PruneReleaseGateProbeResults :exec
DELETE FROM release_gate_probe_results WHERE window_start < sqlc.arg(before)::timestamptz;
