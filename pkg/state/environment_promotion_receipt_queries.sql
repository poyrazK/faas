-- name: ReadEnvironmentPromotionReceipt :one
SELECT attempt_id, request_hash, promotion_hash, project_id::text AS project_id,
       from_environment_id::text AS from_environment_id, environment_id::text AS environment_id,
       promotion_id::text AS promotion_id, accepted_at
FROM project_environment_promotion_receipts
WHERE account_id = sqlc.arg(account_id)::text::uuid AND attempt_id = sqlc.arg(attempt_id);

-- name: InsertEnvironmentPromotionReceipt :exec
INSERT INTO project_environment_promotion_receipts
(account_id, promotion_id, attempt_id, request_hash, promotion_hash, project_id, from_environment_id, environment_id, accepted_at)
VALUES (sqlc.arg(account_id)::text::uuid, sqlc.arg(promotion_id)::text::uuid, sqlc.arg(attempt_id), sqlc.arg(request_hash),
 sqlc.arg(promotion_hash), sqlc.arg(project_id)::text::uuid, sqlc.arg(from_environment_id)::text::uuid,
 sqlc.arg(environment_id)::text::uuid, sqlc.arg(accepted_at));

-- name: LockEnvironmentPromotionAttempt :exec
SELECT pg_advisory_xact_lock(hashtextextended('environment-promotion:' || sqlc.arg(account_id)::text || ':' || sqlc.arg(attempt_id)::text, 0));
