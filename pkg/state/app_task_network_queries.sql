-- ADR-20261118070000: service-mesh caller identity of app task VMs.

-- name: RecordAppTaskNetwork :execrows
INSERT INTO app_task_networks(task_id,node_id,host_ip,lease_token)
SELECT t.id,n.id,sqlc.arg(host_ip)::text::inet,t.lease_token FROM app_tasks t
JOIN compute_nodes n ON n.id=nullif(sqlc.arg(node_id)::text,'')::uuid
 OR (sqlc.arg(node_id)::text='' AND n.name=sqlc.arg(local_name)::text)
WHERE t.id=sqlc.arg(task_id)::text::uuid AND t.lease_token=sqlc.arg(lease_token)::text::uuid
 AND t.status='restoring' AND t.lease_expires_at>clock_timestamp() AND t.cancel_requested_at IS NULL
LIMIT 1
ON CONFLICT (task_id) DO NOTHING;

-- name: RetireAppTaskNetwork :exec
UPDATE app_task_networks SET active=false WHERE task_id=sqlc.arg(task_id)::text::uuid AND active;

-- name: AppTaskCallersByHostIP :many
SELECT t.id::text AS instance_id,t.app_id::text AS app_id,t.deployment_id::text AS deployment_id
FROM app_task_networks c JOIN app_tasks t ON t.id=c.task_id JOIN compute_nodes n ON n.id=c.node_id
JOIN apps a ON a.id=t.app_id
WHERE c.active AND c.host_ip=sqlc.arg(host_ip)::text::inet AND (sqlc.arg(node_name)::text='' OR n.name=sqlc.arg(node_name)::text)
 AND t.lease_token=c.lease_token AND t.lease_expires_at>clock_timestamp() AND t.cancel_requested_at IS NULL
 AND t.status='running' AND a.status<>'deleted'
LIMIT 2;

