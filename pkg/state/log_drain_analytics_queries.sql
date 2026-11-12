-- name: ListLogDrainAnalyticsWithBaseline :many
WITH baseline AS (
 SELECT * FROM app_log_drain_delivery_analytics WHERE drain_id=sqlc.arg(drain_id)::uuid
 AND bucket_start<sqlc.arg(window_from)::timestamptz ORDER BY bucket_start DESC LIMIT 1
), window_samples AS (
 SELECT * FROM app_log_drain_delivery_analytics WHERE drain_id=sqlc.arg(drain_id)::uuid
 AND bucket_start>=sqlc.arg(window_from)::timestamptz AND bucket_start<=sqlc.arg(window_until)::timestamptz
)
SELECT * FROM baseline UNION ALL SELECT * FROM window_samples ORDER BY bucket_start;
