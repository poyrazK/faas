-- name: GetAPIConsumerCreditPackSettings :one
SELECT return_url, packs FROM api_consumer_credit_pack_settings
WHERE account_id = $1 AND app_id = $2;

-- name: PutAPIConsumerCreditPackSettings :exec
INSERT INTO api_consumer_credit_pack_settings (account_id, app_id, return_url, packs)
VALUES ($1, $2, $3, $4)
ON CONFLICT (app_id) DO UPDATE SET return_url = excluded.return_url, packs = excluded.packs
WHERE api_consumer_credit_pack_settings.account_id = excluded.account_id;

-- name: LockAPIConsumerCreditBuyer :one
SELECT id FROM api_consumers
WHERE account_id = sqlc.arg(account_id) AND app_id = sqlc.arg(app_id) AND id = sqlc.arg(id)
  AND (NOT sqlc.arg(require_active)::boolean OR revoked_at IS NULL)
FOR UPDATE;

-- name: CountReservedAPIConsumerCreditGrants :one
SELECT (
    (SELECT count(*) FROM api_consumer_credit_grants WHERE consumer_id = sqlc.arg(buyer_id)::uuid AND remaining_millicents > 0)
    + (SELECT count(*) FROM api_consumer_credit_purchases WHERE consumer_id = sqlc.arg(buyer_id)::uuid AND status = 'pending')
)::bigint;

-- name: GetAPIConsumerCreditPurchase :one
SELECT * FROM api_consumer_credit_purchases WHERE account_id = $1 AND app_id = $2 AND consumer_id = $3 AND id = $4;

-- name: InsertAPIConsumerCreditPurchase :one
INSERT INTO api_consumer_credit_purchases
    (id, account_id, app_id, consumer_id, pack_id, label, currency, amount_millicents, amount_minor, return_url,
     stripe_customer, livemode, expires_at, next_check_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $13)
RETURNING *;

-- name: SetAPIConsumerCreditPurchaseCheckout :one
UPDATE api_consumer_credit_purchases
SET stripe_session = sqlc.arg(stripe_session)::text, next_check_at = sqlc.arg(next_check_at)
WHERE id = sqlc.arg(id) AND status = 'pending' AND (stripe_session IS NULL OR stripe_session = sqlc.arg(stripe_session)::text)
RETURNING *;

-- name: ClaimAPIConsumerCreditPurchases :many
WITH due AS (
    SELECT p.id FROM api_consumer_credit_purchases p
    WHERE p.status = 'pending' AND p.next_check_at <= sqlc.arg(due_at)
    ORDER BY p.next_check_at, p.id LIMIT sqlc.arg(batch_size) FOR UPDATE SKIP LOCKED
)
UPDATE api_consumer_credit_purchases p SET next_check_at = sqlc.arg(next_check_at)
FROM due WHERE p.id = due.id RETURNING p.*;

-- name: ExpireAPIConsumerCreditPurchase :exec
UPDATE api_consumer_credit_purchases SET status = 'expired'
WHERE id = sqlc.arg(id) AND status = 'pending'
  AND ((stripe_session IS NULL AND sqlc.arg(session_id)::text = '') OR stripe_session = sqlc.arg(session_id)::text);

-- name: CreditAPIConsumerPurchase :one
WITH purchase AS (
    SELECT id, account_id, app_id, consumer_id, currency, amount_millicents FROM api_consumer_credit_purchases
    WHERE api_consumer_credit_purchases.id = sqlc.arg(purchase_id)
      AND stripe_session = sqlc.arg(session_id)::text AND status = 'pending'
    FOR UPDATE
), grant_credit AS (
    INSERT INTO api_consumer_credit_grants
        (id, account_id, app_id, consumer_id, currency, amount_millicents, remaining_millicents, note)
    SELECT sqlc.arg(grant_id)::uuid, account_id, app_id, consumer_id, currency, amount_millicents, amount_millicents, 'Stripe credit pack'
    FROM purchase RETURNING id
), recheck_cap AS (
    UPDATE api_consumer_spend_caps c
    SET updated_at = now(), next_check_at = least(c.next_check_at, now())
    FROM purchase p, grant_credit g
    WHERE c.account_id = p.account_id AND c.consumer_id = p.consumer_id AND c.currency = p.currency
)
UPDATE api_consumer_credit_purchases p SET status = 'credited'
FROM grant_credit g WHERE p.id = sqlc.arg(purchase_id) AND g.id = sqlc.arg(grant_id)::uuid RETURNING p.*;
