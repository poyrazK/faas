-- name: RunTaskVersionGet :one
SELECT * FROM execution_task_versions WHERE account_id=$1 AND name=$2 AND version=$3;
-- name: RunTaskVersionAdmission :one
SELECT * FROM execution_task_versions WHERE account_id=$1 AND id=$2 AND status='active' FOR SHARE;
-- name: RunTaskVersionList :many
SELECT id,account_id,name,version,digest,metadata,environment_id,runtime,agent_endpoint_ids,status,''::bytea AS sealed_definition,kid,created_at FROM execution_task_versions WHERE account_id=$1 AND (name>sqlc.arg(after_name)::text OR name=sqlc.arg(after_name)::text AND version<sqlc.arg(after_version)::int) ORDER BY name,version DESC LIMIT sqlc.arg(page_limit)::int;
-- name: RunTaskVersionStats :one
SELECT count(*) AS versions,count(DISTINCT name) AS names,COALESCE(sum(octet_length(sealed_definition)),0)::bigint AS sealed_bytes FROM execution_task_versions WHERE account_id=$1;
-- name: RunTaskNameExists :one
SELECT EXISTS(SELECT 1 FROM execution_task_versions WHERE account_id=$1 AND name=$2);
-- name: RunTaskVersionInsert :one
INSERT INTO execution_task_versions(id,account_id,name,version,digest,metadata,environment_id,runtime,agent_endpoint_ids,sealed_definition,kid) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) RETURNING *;
-- name: RunTaskVersionRetire :execrows
UPDATE execution_task_versions SET status='retired',sealed_definition=''::bytea WHERE account_id=$1 AND name=$2 AND version=$3;
-- name: RunTaskEndpointsOwned :one
SELECT count(*)::bigint=cardinality(sqlc.arg(endpoint_ids)::uuid[]) FROM managed_realtime_endpoints WHERE account_id=$1 AND id=ANY(sqlc.arg(endpoint_ids)::uuid[]);
-- name: RunTaskInvocationInsert :exec
INSERT INTO execution_task_invocations(execution_id,account_id,version_id,request_digest,callback_key,callback) VALUES ($1,$2,$3,$4,$5,$6);
-- name: RunTaskInvocationGet :one
SELECT i.*,v.name,v.version,v.digest FROM execution_task_invocations i JOIN execution_task_versions v ON v.id=i.version_id WHERE i.account_id=$1 AND i.execution_id=$2;
-- name: RunTaskCallbackReplay :one
SELECT execution_id,request_digest FROM execution_task_invocations WHERE account_id=$1 AND callback_key=$2;
-- name: RunTaskCallbacksPending :many
SELECT i.*,v.name,v.version,v.digest FROM execution_task_invocations i JOIN execution_task_versions v ON v.id=i.version_id JOIN executions e ON e.id=i.execution_id WHERE i.callback_key IS NOT NULL AND i.delivered_at IS NULL AND e.status IN ('succeeded','failed','timed_out','out_of_memory','cancelled') AND (sqlc.arg(after_id)::text='' OR i.execution_id>NULLIF(sqlc.arg(after_id)::text,'')::uuid) ORDER BY i.execution_id LIMIT sqlc.arg(batch_limit)::int;
-- name: RunTaskCallbackDelivered :execrows
UPDATE execution_task_invocations SET delivered_at=COALESCE(delivered_at,clock_timestamp()) WHERE account_id=$1 AND execution_id=$2 AND callback_key IS NOT NULL;
-- name: RunTaskInvocationRefs :many
SELECT i.execution_id,v.id,v.name,v.version,v.digest FROM execution_task_invocations i JOIN execution_task_versions v ON v.id=i.version_id WHERE i.account_id=$1 AND i.execution_id=ANY(sqlc.arg(execution_ids)::uuid[]);
