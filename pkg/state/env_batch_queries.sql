-- name: ReadEnvBatchReceipt :one
SELECT request_sha256,receipt FROM app_env_batch_receipts
WHERE account_id=sqlc.arg(account_id)::uuid AND app_id=sqlc.arg(app_id)::uuid
 AND scope=sqlc.arg(scope)::text AND request_id=sqlc.arg(request_id)::uuid;

-- name: WriteEnvBatchReceipt :exec
INSERT INTO app_env_batch_receipts(app_id,scope,request_id,account_id,request_sha256,receipt)
VALUES(sqlc.arg(app_id)::uuid,sqlc.arg(scope)::text,sqlc.arg(request_id)::uuid,
 sqlc.arg(account_id)::uuid,sqlc.arg(request_sha256)::bytea,sqlc.arg(receipt)::jsonb);

-- name: PruneEnvBatchReceipts :exec
DELETE FROM app_env_batch_receipts WHERE app_id=sqlc.arg(app_id)::uuid AND created_at<sqlc.arg(before)::timestamptz;

-- name: LockEnvBatchOwner :one
SELECT id FROM apps WHERE id=sqlc.arg(app_id)::uuid AND account_id=sqlc.arg(account_id)::uuid AND status<>'deleted' FOR UPDATE;

-- name: WriteEnvBatchVariable :exec
INSERT INTO app_envs(account_id,app_id,scope,key,value)
VALUES(sqlc.arg(account_id)::uuid,sqlc.arg(app_id)::uuid,sqlc.arg(scope)::text,sqlc.arg(key)::text,sqlc.arg(value)::text)
ON CONFLICT(app_id,scope,key) DO UPDATE SET value=excluded.value,updated_at=clock_timestamp();

-- name: DeleteEnvBatchVariable :exec
DELETE FROM app_envs WHERE account_id=sqlc.arg(account_id)::uuid AND app_id=sqlc.arg(app_id)::uuid
 AND scope=sqlc.arg(scope)::text AND key=sqlc.arg(key)::text;

-- name: InvalidateEnvBatchSnapshots :exec
WITH stamped AS (
 INSERT INTO app_runtime_config_scope_changes(app_id,scope,changed_at)
 VALUES(sqlc.arg(app_id)::uuid,sqlc.arg(scope)::text,clock_timestamp())
 ON CONFLICT(app_id,scope) DO UPDATE SET changed_at=greatest(app_runtime_config_scope_changes.changed_at,excluded.changed_at)
 RETURNING app_id,scope
), global_stamp AS (
 INSERT INTO app_runtime_config_changes(app_id,changed_at)
 SELECT app_id,clock_timestamp() FROM stamped WHERE scope='default'
 ON CONFLICT(app_id) DO UPDATE SET changed_at=greatest(app_runtime_config_changes.changed_at,excluded.changed_at)
)
UPDATE snapshots SET stale=true FROM deployments d,stamped s
WHERE snapshots.deployment_id=d.id AND d.app_id=s.app_id
 AND (s.scope='default' OR d.scope=s.scope) AND NOT snapshots.stale;
