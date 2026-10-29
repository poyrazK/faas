-- name: ListAppWorkloadOOMKills :many
-- ADR-121 and ADR-1447 amendments: the app's OOM kills in a window, newest
-- first, with the window's total, for GET /v1/apps/{slug}/debug/status. Kills
-- are rare, so events_kind_at_idx (kind, at DESC) bounds the scan.
SELECT at, data, count(*) OVER () AS total
  FROM events
 WHERE kind = 'instances.workload_oom_failed'
   AND at >= sqlc.arg(since)::timestamptz
   AND at <= sqlc.arg(until)::timestamptz
   AND data ? 'app_id'
   AND data->>'app_id' = sqlc.arg(app_id)::text
 ORDER BY at DESC, id DESC
 LIMIT sqlc.arg(row_limit)::int;
