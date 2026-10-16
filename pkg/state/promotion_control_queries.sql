-- name: ReadPromotionControl :one
SELECT payload FROM project_environment_promotion_controls
WHERE promotion_id=sqlc.arg(promotion_id)::text::uuid AND request_id=sqlc.arg(request_id)::text;

-- name: InsertPromotionControl :exec
INSERT INTO project_environment_promotion_controls(promotion_id,request_id,payload)
VALUES(sqlc.arg(promotion_id)::text::uuid,sqlc.arg(request_id)::text,sqlc.arg(payload)::jsonb);

-- name: UpdatePromotionControl :execrows
UPDATE project_environment_promotion_controls SET payload=sqlc.arg(payload)::jsonb
WHERE promotion_id=sqlc.arg(promotion_id)::text::uuid AND request_id=sqlc.arg(request_id)::text;

-- name: PromotionControlBaselineUsable :one
SELECT EXISTS(SELECT 1 FROM project_release_sets WHERE id=sqlc.arg(release_id)::text::uuid AND (active OR expires_at>clock_timestamp())) AS usable;

-- name: RetireAbortedPromotionCandidates :exec
UPDATE deployments d SET status='superseded'
FROM project_environment_promotion_workload_specs pin
WHERE pin.promotion_id=sqlc.arg(promotion_id)::text::uuid AND pin.deployment_id=d.id AND pin.app_id=d.app_id
AND d.traffic_percent_explicit AND d.traffic_percent=0 AND d.status IN ('pending','live')
AND NOT EXISTS(SELECT 1 FROM project_release_members m JOIN project_release_sets r ON r.id=m.release_id WHERE m.deployment_id=d.id AND r.active);

-- name: PromotionPreparationOwner :one
SELECT account_id::text,project_id::text FROM project_environment_promotions WHERE id=sqlc.arg(promotion_id)::text::uuid;

-- name: AuthorizePromotionControlRollback :exec
SELECT set_config('faas.promotion_control_publication',sqlc.arg(promotion_id)::text || ':' || sqlc.arg(generation)::bigint::text || ':' || sqlc.arg(request_id)::text,true);
