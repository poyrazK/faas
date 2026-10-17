-- name: PromotionWorkerDeployment :one
SELECT coalesce((SELECT deployment_id::text FROM project_environment_promotion_workload_specs
 WHERE promotion_id=sqlc.arg(promotion_id)::text::uuid AND app_id=sqlc.arg(app_id)::text::uuid),'')::text AS deployment_id;

-- name: LockPromotionRecoveryWorkerInstances :many
SELECT id::text FROM instances
WHERE deployment_id=sqlc.arg(deployment_id)::text::uuid AND mode='worker'
ORDER BY id FOR SHARE;

-- name: LockPromotionRecoveryWorkerConsumer :many
SELECT b.id::text FROM queue_bindings b JOIN triggers t ON t.queue_binding_id=b.id
WHERE b.id=sqlc.arg(binding_id)::text::uuid
ORDER BY t.id FOR SHARE OF b,t;

-- name: SetPromotionWorkerSetPublicationGuard :exec
SELECT set_config('faas.promotion_worker_set_guard',sqlc.arg(token)::text,true);
