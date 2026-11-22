-- Route resource peaks (ADR-20261121170000).

-- name: AddInstanceResourceMinutes :exec
-- The arrays are parallel. Rows of one batch never share a key: vmmd
-- sends each instance-minute once per batch.
INSERT INTO instance_resource_minutes AS t
    (app_id, instance_id, minute, account_id, deployment_id, samples,
     mem_start_mb, mem_peak_mb, mem_end_mb, mem_limit_mb,
     cpu_ms, cpu_peak_millicores, cpu_limit_millicores)
SELECT unnest(sqlc.arg(app_ids)::uuid[]),
       unnest(sqlc.arg(instance_ids)::text[]),
       unnest(sqlc.arg(minutes)::timestamptz[]),
       unnest(sqlc.arg(account_ids)::uuid[]),
       unnest(sqlc.arg(deployment_ids)::uuid[]),
       unnest(sqlc.arg(samples)::int[]),
       unnest(sqlc.arg(mem_start_mb)::int[]),
       unnest(sqlc.arg(mem_peak_mb)::int[]),
       unnest(sqlc.arg(mem_end_mb)::int[]),
       unnest(sqlc.arg(mem_limit_mb)::int[]),
       unnest(sqlc.arg(cpu_ms)::bigint[]),
       unnest(sqlc.arg(cpu_peak_millicores)::int[]),
       unnest(sqlc.arg(cpu_limit_millicores)::int[])
ON CONFLICT (app_id, minute, instance_id) DO UPDATE SET
    samples = t.samples + EXCLUDED.samples,
    mem_peak_mb = GREATEST(t.mem_peak_mb, EXCLUDED.mem_peak_mb),
    mem_end_mb = LEAST(EXCLUDED.mem_end_mb, GREATEST(t.mem_peak_mb, EXCLUDED.mem_peak_mb)),
    mem_limit_mb = GREATEST(t.mem_limit_mb, EXCLUDED.mem_limit_mb),
    cpu_ms = t.cpu_ms + EXCLUDED.cpu_ms,
    cpu_peak_millicores = GREATEST(t.cpu_peak_millicores, EXCLUDED.cpu_peak_millicores),
    cpu_limit_millicores = GREATEST(t.cpu_limit_millicores, EXCLUDED.cpu_limit_millicores);

-- name: AddInstanceRouteMinutes :exec
INSERT INTO instance_route_minutes AS t
    (app_id, instance_id, minute, route, method, account_id, requests, busy_ms, max_latency_ms)
SELECT unnest(sqlc.arg(app_ids)::uuid[]),
       unnest(sqlc.arg(instance_ids)::text[]),
       unnest(sqlc.arg(minutes)::timestamptz[]),
       unnest(sqlc.arg(routes)::text[]),
       unnest(sqlc.arg(methods)::text[]),
       unnest(sqlc.arg(account_ids)::uuid[]),
       unnest(sqlc.arg(requests)::bigint[]),
       unnest(sqlc.arg(busy_ms)::bigint[]),
       unnest(sqlc.arg(max_latency_ms)::int[])
ON CONFLICT (app_id, minute, instance_id, route, method) DO UPDATE SET
    requests = t.requests + EXCLUDED.requests,
    busy_ms = t.busy_ms + EXCLUDED.busy_ms,
    max_latency_ms = GREATEST(t.max_latency_ms, EXCLUDED.max_latency_ms);

-- name: ListInstanceResourceMinutes :many
SELECT instance_id, minute, samples, mem_start_mb, mem_peak_mb, mem_end_mb, mem_limit_mb,
       cpu_ms, cpu_peak_millicores, cpu_limit_millicores
FROM instance_resource_minutes
WHERE app_id = sqlc.arg(app_id) AND minute >= sqlc.arg(from_minute) AND minute < sqlc.arg(to_minute)
ORDER BY minute DESC, instance_id
LIMIT sqlc.arg(row_limit);

-- name: ListInstanceRouteMinutes :many
SELECT instance_id, minute, route, method, requests, busy_ms, max_latency_ms
FROM instance_route_minutes
WHERE app_id = sqlc.arg(app_id) AND minute >= sqlc.arg(from_minute) AND minute < sqlc.arg(to_minute)
ORDER BY minute DESC, instance_id, route, method
LIMIT sqlc.arg(row_limit);
