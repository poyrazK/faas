-- name: ExecutionEnvironmentExisting :one
SELECT * FROM execution_environments WHERE account_id=$1 AND principal_id=$2 AND recipe_digest=$3 AND status!='deleted';
-- name: ExecutionEnvironmentCount :one
SELECT count(*) FROM execution_environments WHERE account_id=$1 AND status!='deleted';
-- name: ExecutionEnvironmentInsert :one
INSERT INTO execution_environments(id,account_id,principal_id,runtime,recipe,recipe_digest,base_ref,status) VALUES ($1,$2,$3,$4,$5,$6,$7,sqlc.arg(initial_status)) RETURNING *;
-- name: ExecutionEnvironmentGet :one
SELECT * FROM execution_environments WHERE account_id=$1 AND id=$2 AND status!='deleted';
-- name: ExecutionEnvironmentList :many
SELECT * FROM execution_environments WHERE account_id=$1 AND (sqlc.arg(wide)::boolean OR principal_id=sqlc.arg(principal_id)) AND status!='deleted' ORDER BY created_at DESC LIMIT 100;
-- name: ExecutionEnvironmentRetire :execrows
UPDATE execution_environments SET status='retiring' WHERE account_id=$1 AND id=$2 AND (sqlc.arg(wide)::boolean OR principal_id=sqlc.arg(principal_id)) AND status!='deleted';
-- name: ExecutionEnvironmentExpireClaims :exec
UPDATE execution_environments SET status='failed', failure_code='worker_recovery_exhausted', lease_expires_at=NULL
WHERE lease_expires_at < clock_timestamp() AND ((status IN ('building','building_system_v1') AND build_attempts >= 3) OR (status IN ('imaging','imaging_system_v1') AND image_attempts >= 3));
-- name: ExecutionEnvironmentClaim :one
WITH candidate AS (
 SELECT pending.id, pending.status IN (sqlc.arg(system_queued_status),sqlc.arg(system_running_status)) AS system_lane FROM execution_environments pending WHERE
 ((pending.status IN (sqlc.arg(queued_status),sqlc.arg(system_queued_status)) OR pending.status IN (sqlc.arg(running_status),sqlc.arg(system_running_status)) AND pending.lease_expires_at < clock_timestamp()))
 AND CASE WHEN sqlc.arg(build)::boolean THEN pending.build_attempts ELSE pending.image_attempts END < 3
 AND EXISTS (SELECT 1 FROM accounts WHERE accounts.id=pending.account_id)
 ORDER BY pending.created_at FOR UPDATE OF pending SKIP LOCKED LIMIT 1
), claimed AS (
 UPDATE execution_environments e SET status=CASE WHEN c.system_lane THEN sqlc.arg(system_running_status)::text ELSE sqlc.arg(running_status)::text END,lease=sqlc.arg(lease),
 lease_expires_at=clock_timestamp()+make_interval(secs=>sqlc.arg(lease_seconds)::int),
 build_attempts=build_attempts+CASE WHEN sqlc.arg(build)::boolean THEN 1 ELSE 0 END,
 image_attempts=image_attempts+CASE WHEN sqlc.arg(build)::boolean THEN 0 ELSE 1 END,
 export_key=CASE WHEN sqlc.arg(build)::boolean THEN 'run-environments/'||account_id||'/'||e.id||'/'||sqlc.arg(lease)||'/image.tar' ELSE export_key END,
 base_key=CASE WHEN sqlc.arg(build)::boolean THEN base_key ELSE 'run-environments/'||account_id||'/'||e.id||'/'||sqlc.arg(lease)||'/base.ext4' END,
 artifact_keys=artifact_keys || ARRAY['run-environments/'||account_id||'/'||e.id||'/'||sqlc.arg(lease)||CASE WHEN sqlc.arg(build)::boolean THEN '/image.tar' ELSE '/base.ext4' END] || CASE WHEN sqlc.arg(build)::boolean THEN ARRAY[]::text[] ELSE ARRAY['run-environments/'||account_id||'/'||e.id||'/'||sqlc.arg(lease)||'/layer.ext4','run-environments/'||account_id||'/'||e.id||'/'||sqlc.arg(lease)||'/base.ext4.digest','run-environments/'||account_id||'/'||e.id||'/'||sqlc.arg(lease)||'/base.ext4.content','scans/run-environments/'||account_id||'/'||e.id||'/'||sqlc.arg(lease)||'/base.ext4.scan.json'] END
 FROM candidate c WHERE e.id=c.id RETURNING e.*
) SELECT * FROM claimed;
-- name: ExecutionEnvironmentPublishBuild :execrows
UPDATE execution_environments SET status=CASE WHEN status='building_system_v1' THEN 'built_system_v1' ELSE 'built' END,export_digest=$3,lease_expires_at=NULL WHERE id=$1 AND lease=$2 AND status IN ('building','building_system_v1') AND lease_expires_at>clock_timestamp();
-- name: ExecutionEnvironmentPublishImage :execrows
UPDATE execution_environments SET status='ready',image_digest=$3,artifact_bytes=$4,guest_executor_digest=sqlc.arg(guest_executor_digest),lease_expires_at=NULL WHERE id=$1 AND lease=$2 AND status IN ('imaging','imaging_system_v1') AND lease_expires_at>clock_timestamp();
-- name: ExecutionEnvironmentFail :execrows
UPDATE execution_environments SET status='failed',failure_code=$3,lease_expires_at=NULL WHERE id=$1 AND lease=$2 AND status IN ('building','imaging','building_system_v1','imaging_system_v1') AND lease_expires_at>clock_timestamp();
-- name: ExecutionEnvironmentCollectable :many
WITH candidates AS (
 SELECT e.id FROM execution_environments e WHERE
 (e.status='retiring' OR NOT EXISTS (SELECT 1 FROM accounts a WHERE a.id=e.account_id))
 AND e.status!='deleted' AND (e.lease_expires_at IS NULL OR e.lease_expires_at<clock_timestamp())
 AND NOT EXISTS (SELECT 1 FROM executions r WHERE r.environment_id=e.id AND r.status IN ('queued','restoring','running'))
 ORDER BY e.created_at LIMIT sqlc.arg(batch_limit)::int FOR UPDATE OF e SKIP LOCKED
) UPDATE execution_environments e SET status='retiring' FROM candidates c WHERE e.id=c.id RETURNING e.*;
-- name: ExecutionEnvironmentDeleted :execrows
UPDATE execution_environments e SET status='deleted',recipe='{}',artifact_bytes=0 WHERE e.id=$1 AND e.status='retiring' AND (e.lease_expires_at IS NULL OR e.lease_expires_at<clock_timestamp()) AND NOT EXISTS (SELECT 1 FROM executions r WHERE r.environment_id=e.id AND r.status IN ('queued','restoring','running'));

-- name: ExecutionEnvironmentAdmission :one
SELECT * FROM execution_environments WHERE account_id=$1 AND id=$2 AND status='ready' FOR SHARE;


-- name: ExecutionEnvironmentReadDiagnostics :one
SELECT COALESCE(d.diagnostic_sequence,0)::bigint AS diagnostic_sequence, COALESCE(d.diagnostics,'[]'::jsonb)::jsonb AS diagnostics
FROM execution_environments e LEFT JOIN execution_environment_diagnostics d ON d.environment_id=e.id
WHERE e.id=$1 AND e.account_id=$2 AND e.status!='deleted';

-- name: ExecutionEnvironmentAppendDiagnostic :execrows
WITH owner AS (
 SELECT e.id,e.status,e.build_attempts,e.image_attempts FROM execution_environments e
 WHERE e.id=sqlc.arg(id) AND e.account_id=sqlc.arg(account_id) AND e.lease=sqlc.arg(lease) AND e.status=sqlc.arg(status) AND e.status IN ('building','imaging','building_system_v1','imaging_system_v1') AND e.lease_expires_at>clock_timestamp() FOR UPDATE
)
INSERT INTO execution_environment_diagnostics AS d(environment_id,diagnostic_sequence,diagnostics)
SELECT o.id,1,jsonb_build_array(jsonb_build_object('sequence',1,'stage',sqlc.arg(stage)::text,'code',sqlc.arg(code)::text,'message',sqlc.arg(message)::text,'attempt',CASE WHEN o.status IN ('building','building_system_v1') THEN o.build_attempts ELSE o.image_attempts END,'created_at',to_char(clock_timestamp() AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"'))) FROM owner o
ON CONFLICT(environment_id) DO UPDATE SET diagnostic_sequence=d.diagnostic_sequence+1,
 diagnostics=(SELECT COALESCE(jsonb_agg(value ORDER BY ordinal),'[]'::jsonb) FROM jsonb_array_elements(d.diagnostics || jsonb_set(EXCLUDED.diagnostics->0,'{sequence}',to_jsonb(d.diagnostic_sequence+1))) WITH ORDINALITY AS item(value,ordinal) WHERE ordinal>jsonb_array_length(d.diagnostics)+1-sqlc.arg(max_events)::int);
