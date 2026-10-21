-- name: ReadPromotionRecovery :one
SELECT r.payload FROM project_environment_promotion_migration_recoveries r JOIN project_environment_promotions p ON p.id=r.promotion_id
WHERE p.account_id=sqlc.arg(account_id)::text::uuid AND p.id=sqlc.arg(promotion_id)::text::uuid AND r.request_id=sqlc.arg(request_id)::text;

-- name: LatestPromotionRecovery :one
SELECT r.payload FROM project_environment_promotion_migration_recoveries r JOIN project_environment_promotions p ON p.id=r.promotion_id
WHERE p.account_id=sqlc.arg(account_id)::text::uuid AND p.id=sqlc.arg(promotion_id)::text::uuid ORDER BY (r.payload#>>'{receipt,request,expected_generation}')::bigint DESC LIMIT 1;

-- name: InsertPromotionRecovery :exec
INSERT INTO project_environment_promotion_migration_recoveries(promotion_id,request_id,request_hash,status,payload)
VALUES(sqlc.arg(promotion_id)::text::uuid,sqlc.arg(request_id)::text,sqlc.arg(request_hash)::text,'quiescing',sqlc.arg(payload)::jsonb);

-- name: SavePromotionRecovery :execrows
UPDATE project_environment_promotion_migration_recoveries SET status=sqlc.arg(status)::text,payload=sqlc.arg(payload)::jsonb
WHERE promotion_id=sqlc.arg(promotion_id)::text::uuid AND request_id=sqlc.arg(request_id)::text AND status=sqlc.arg(previous_status)::text;

-- name: RegisterPromotionMigrationRuntime :execrows
UPDATE project_environment_promotion_migrations SET runtime_node_id=sqlc.arg(node_id)::text::uuid,runtime_instance_id=sqlc.arg(instance_id)::text::uuid,runtime_lease_token=sqlc.arg(token)::text::uuid
WHERE task_id=sqlc.arg(task_id)::text::uuid AND prepared_lease_token=sqlc.arg(token)::text::uuid
AND (runtime_lease_token IS DISTINCT FROM sqlc.arg(token)::text::uuid OR runtime_node_id=sqlc.arg(node_id)::text::uuid AND runtime_instance_id=sqlc.arg(instance_id)::text::uuid);

-- name: ReadPromotionMigrationRuntime :one
SELECT coalesce(runtime_node_id::text,'')::text AS node_id,coalesce(runtime_instance_id::text,'')::text AS instance_id,coalesce(runtime_lease_token::text,'')::text AS lease_token,destroyed_at
FROM project_environment_promotion_migrations WHERE promotion_id=sqlc.arg(promotion_id)::text::uuid;

-- name: MarkPromotionMigrationDestroyed :exec
UPDATE project_environment_promotion_migrations SET destroyed_at=coalesce(destroyed_at,now()) WHERE promotion_id=sqlc.arg(promotion_id)::text::uuid;

-- name: PendingPromotionRecoveryQuiescence :many
SELECT p.id::text AS promotion_id,p.account_id::text AS account_id,r.request_id,m.task_id::text AS task_id,
coalesce(m.runtime_node_id::text,'')::text AS node_id,coalesce(m.runtime_instance_id::text,'')::text AS instance_id,coalesce(m.runtime_lease_token::text,'')::text AS lease_token
FROM project_environment_promotion_migration_recoveries r JOIN project_environment_promotions p ON p.id=r.promotion_id
JOIN project_environment_promotion_migrations m ON m.promotion_id=p.id JOIN app_tasks t ON t.id=m.task_id
WHERE r.status='quiescing' AND p.status='failed' AND t.status IN ('failed','timed_out','cancelled')
AND m.runtime_lease_token=m.prepared_lease_token AND m.runtime_node_id IS NOT NULL
ORDER BY r.created_at LIMIT sqlc.arg(batch_size)::int;

-- name: ReadPromotionRecoveryTask :one
SELECT * FROM app_tasks WHERE id=sqlc.arg(task_id)::text::uuid;

-- name: PromotionRecoveryLaneAllowed :one
SELECT NOT EXISTS(SELECT 1 FROM project_environment_promotions p JOIN project_environment_promotion_migrations m ON m.promotion_id=p.id
 WHERE p.project_id=sqlc.arg(project_id)::text::uuid AND p.id<>sqlc.arg(promotion_id)::text::uuid
 AND (p.status='running' OR EXISTS(SELECT 1 FROM project_environment_promotion_migration_recoveries r WHERE r.promotion_id=p.id AND r.status IN ('quiescing','verifying'))))
AND NOT EXISTS(SELECT 1 FROM app_tasks t JOIN apps a ON a.id=t.app_id WHERE a.project_id=sqlc.arg(project_id)::text::uuid AND t.id<>sqlc.arg(task_id)::text::uuid AND t.kind='release'
 AND (t.status IN ('queued','restoring','running') OR t.started_at IS NOT NULL AND t.exit_code IS NULL AND NOT EXISTS(SELECT 1 FROM project_promotion_migration_resolved_tasks resolved WHERE resolved.task_id=t.id)))
AND NOT EXISTS(SELECT 1 FROM project_submissions s WHERE s.payload#>>'{apply,project_id}'=sqlc.arg(project_id)::text AND s.payload#>>'{apply,safe_release,release_command,compatibility}'='backward-compatible' AND s.payload#>>'{apply,safe_release,status}' IN ('building','verifying','rolling_back'))
AND NOT EXISTS(SELECT 1 FROM project_migration_pending_contracts WHERE project_id=sqlc.arg(project_id)::text::uuid) AS allowed;
