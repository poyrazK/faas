-- adr: 801
-- name: RegionalRuntimeFloorHistoryStart :one
SELECT started_at FROM regional_runtime_floor_history_start WHERE singleton=true;

-- name: ListRegionalRuntimeFloorIntervals :many
SELECT * FROM regional_runtime_floor_intervals
WHERE app_id=sqlc.arg(app_id) AND id>sqlc.arg(after_id)
 AND started_at<sqlc.arg(minute_end)::timestamptz
 AND (ended_at IS NULL OR ended_at>sqlc.arg(minute_start)::timestamptz)
ORDER BY id LIMIT sqlc.arg(batch_limit);
