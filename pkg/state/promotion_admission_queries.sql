-- name: LockPromotionAdmission :exec
SELECT pg_advisory_xact_lock(hashtextextended('promotion-admission',0));

-- name: ReadPromotionAdmissionCounts :one
SELECT
 count(*) FILTER(WHERE h.phase<>'queued')::bigint AS fleet_active,
 count(*) FILTER(WHERE h.phase<>'queued' AND p.account_id=sqlc.arg(account_id)::text::uuid)::bigint AS account_active,
 count(*) FILTER(WHERE h.phase='queued')::bigint AS fleet_queued,
 count(*) FILTER(WHERE h.phase='queued' AND p.account_id=sqlc.arg(account_id)::text::uuid)::bigint AS account_queued,
 count(*) FILTER(WHERE h.phase='queued' AND p.account_id=sqlc.arg(account_id)::text::uuid AND (p.created_at,p.id)<(SELECT created_at,id FROM project_environment_promotions WHERE id=sqlc.arg(promotion_id)::text::uuid))::bigint AS older
FROM project_promotion_health_work w JOIN project_environment_promotion_health h ON h.promotion_id=w.promotion_id
JOIN project_environment_promotions p ON p.id=w.promotion_id;

-- name: AuthorizePromotionQueueDispatch :exec
SELECT set_config('faas.promotion_queue_dispatch',sqlc.arg(promotion_id)::text || ':' || sqlc.arg(generation)::bigint::text || ':' || sqlc.arg(lease_token)::text,true);

-- name: ConsumePromotionAdmissionApproval :one
UPDATE project_environment_approvals SET consumed_at=clock_timestamp()
WHERE account_id=sqlc.arg(account_id)::text::uuid AND project_slug=sqlc.arg(project_slug)::text
AND environment_slug=sqlc.arg(environment)::text AND plan_token_hash=sqlc.arg(plan_token_hash)::text
AND approval_token_hash=sqlc.arg(approval_token_hash)::text AND consumed_at IS NULL AND expires_at>clock_timestamp()
RETURNING id::text,token_kind,consumed_at;
