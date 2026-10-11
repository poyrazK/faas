-- adr: 800
-- name: DeleteAppTasksForPurge :exec
DELETE FROM app_tasks WHERE app_id=sqlc.arg(app_id);

-- name: RegionalAppTaskBillingHistoryStart :one
SELECT started_at FROM regional_app_task_billing_history_start WHERE singleton=true;

-- name: ListRegionalAppTaskBillingIntervals :many
WITH history AS MATERIALIZED (
 SELECT h.* FROM regional_app_task_billing_intervals h
 WHERE h.app_id=sqlc.arg(app_id) AND h.id > sqlc.arg(after_id)
  AND h.started_at < sqlc.arg(minute_end)::timestamptz
  AND (h.ended_at IS NULL OR h.ended_at > sqlc.arg(minute_start)::timestamptz)
 ORDER BY h.id LIMIT sqlc.arg(batch_limit)
)
SELECT history.*, original.permit_claims, original.issued_at,
 original.instance_key, original.resource_claims, original.source_task_id,
 original.source_created_at, COALESCE(original.source_matches,0)::bigint AS source_matches
FROM history
LEFT JOIN LATERAL (
 SELECT c.permit_claims,c.issued_at,s.instance_key,s.resource_claims,s.task_id AS source_task_id,
  s.created_at AS source_created_at,count(*) OVER () AS source_matches
 FROM regional_runtime_capacity_receipts c
 LEFT JOIN regional_runtime_launch_sources s USING (app_id,permit_id)
 WHERE c.app_id=history.app_id AND c.deployment_id=history.deployment_id
  AND c.run_id=history.run_id AND c.kind='app_task' AND c.issued_at IS NOT NULL
 ORDER BY c.permit_id LIMIT 1
) original ON true
ORDER BY history.id;
