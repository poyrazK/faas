-- name: LockPromotionHealthDispatch :exec
SELECT pg_advisory_xact_lock(hashtextextended('promotion-health-dispatch',0));

-- name: PromotionHealthSlotsAvailable :one
SELECT (SELECT count(*) FROM project_environment_promotion_health WHERE lease_token IS NOT NULL AND lease_until>clock_timestamp())<sqlc.arg(concurrency)::int
AND (SELECT count(*) FROM project_environment_promotion_health h JOIN project_environment_promotions p ON p.id=h.promotion_id WHERE h.lease_token IS NOT NULL AND h.lease_until>clock_timestamp() AND p.account_id=sqlc.arg(account_id)::text::uuid)<sqlc.arg(account_concurrency)::int AS available;

-- name: ClaimNextPromotionHealth :one
WITH candidate AS MATERIALIZED (
 SELECT w.promotion_id,w.account_id,w.project_slug,w.to_environment
 FROM project_promotion_health_work w JOIN project_environment_promotion_health h ON h.promotion_id=w.promotion_id
 WHERE h.next_check_at<=clock_timestamp() AND h.lease_until<=clock_timestamp()
 AND (SELECT count(*) FROM project_environment_promotion_health WHERE lease_token IS NOT NULL AND lease_until>clock_timestamp())<sqlc.arg(concurrency)::int
 AND (SELECT count(*) FROM project_environment_promotion_health leased JOIN project_environment_promotions p ON p.id=leased.promotion_id WHERE leased.lease_token IS NOT NULL AND leased.lease_until>clock_timestamp() AND p.account_id=w.account_id)<sqlc.arg(account_concurrency)::int
 ORDER BY CASE WHEN sqlc.arg(prefer_recovery)::boolean AND w.urgent THEN 0 ELSE 1 END,h.next_check_at,h.last_claimed_at,w.promotion_id
 FOR UPDATE OF h SKIP LOCKED LIMIT 1
), claimed AS (
 UPDATE project_environment_promotion_health h SET lease_token=sqlc.arg(lease_token)::text::uuid,
 lease_until=clock_timestamp()+make_interval(secs=>sqlc.arg(lease_seconds)::double precision)
 FROM candidate c WHERE c.promotion_id=h.promotion_id RETURNING h.promotion_id
)
SELECT c.promotion_id::text AS promotion_id,c.account_id::text AS account_id,c.project_slug,c.to_environment FROM candidate c JOIN claimed ON claimed.promotion_id=c.promotion_id;

-- name: PromotionHealthLeaseCurrent :one
SELECT EXISTS(SELECT 1 FROM project_environment_promotion_health h JOIN project_environment_promotions p ON p.id=h.promotion_id WHERE h.promotion_id=sqlc.arg(promotion_id)::text::uuid AND p.account_id=sqlc.arg(account_id)::text::uuid AND h.lease_token=sqlc.arg(lease_token)::text::uuid AND h.lease_until>clock_timestamp()) AS current;

-- name: LockPromotionHealthSchedule :one
SELECT p.account_id::text AS account_id,h.payload FROM project_environment_promotion_health h JOIN project_environment_promotions p ON p.id=h.promotion_id
WHERE h.promotion_id=sqlc.arg(promotion_id)::text::uuid AND h.lease_token=sqlc.arg(lease_token)::text::uuid AND h.lease_until>clock_timestamp() FOR UPDATE OF h;
