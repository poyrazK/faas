-- name: ListSelectedTraceRequests :many
-- Filter account AND explicitly selected app IDs before ordering/limiting.
-- Never read spans_summary, headers, request bodies or infrastructure metadata.
SELECT app_id, id, deployment_id, route, method, status, latency_ms, count,
       cold_boot, received_at
FROM request_telemetry
WHERE account_id = sqlc.arg(account_id)::uuid
  AND app_id = ANY(sqlc.arg(app_ids)::uuid[])
  AND trace_id = sqlc.arg(trace_id)::text
  AND received_at >= sqlc.arg(received_from)::timestamptz
  AND received_at < sqlc.arg(received_until)::timestamptz
ORDER BY received_at ASC, id ASC
LIMIT sqlc.arg(row_limit)::int;

-- name: ListSelectedTraceInvocations :many
-- Durable lifecycle history is independent of request-telemetry retention.
-- Payloads and arbitrary headers are not fetched; the canonical trace header
-- is used only as an indexed predicate.
SELECT app_id, id, source::text AS source, state::text AS state, attempts,
       created_at, received_at, completed_at
FROM invocations
WHERE account_id = sqlc.arg(account_id)::uuid
  AND app_id = ANY(sqlc.arg(app_ids)::uuid[])
  AND headers->>'X-Gregale-Trace-Id' = sqlc.arg(trace_id)::text
ORDER BY created_at ASC, id ASC
LIMIT sqlc.arg(row_limit)::int;
