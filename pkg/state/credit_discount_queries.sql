-- ADR-20261104212028: account credit applied as a provider discount.

-- name: CreateCreditDiscount :one
INSERT INTO credit_discounts (subject_id, provider, provider_subscription_id, provider_discount_id, amount_cents, currency)
VALUES (sqlc.arg(subject_id)::uuid, sqlc.arg(provider)::text, sqlc.arg(provider_subscription_id)::text,
        sqlc.arg(provider_discount_id)::text, sqlc.arg(amount_cents)::bigint, sqlc.arg(currency)::text)
RETURNING id::text AS id, created_at;

-- The subject's discounts still attached, newest first.
-- name: AttachedCreditDiscounts :many
SELECT id::text AS id, subject_id::text AS subject_id, provider, provider_subscription_id, provider_discount_id,
       amount_cents, currency, state, COALESCE(provider_invoice_id, '')::text AS provider_invoice_id,
       COALESCE(redeemed_cents, 0)::bigint AS redeemed_cents, created_at
FROM credit_discounts
WHERE subject_id = sqlc.arg(subject_id)::uuid AND provider = sqlc.arg(provider)::text AND state = 'attached'
ORDER BY created_at DESC, id DESC;

-- name: CreditDiscountByProviderID :one
SELECT id::text AS id, subject_id::text AS subject_id, provider, provider_subscription_id, provider_discount_id,
       amount_cents, currency, state, COALESCE(provider_invoice_id, '')::text AS provider_invoice_id,
       COALESCE(redeemed_cents, 0)::bigint AS redeemed_cents, created_at
FROM credit_discounts
WHERE provider = sqlc.arg(provider)::text AND provider_discount_id = sqlc.arg(provider_discount_id)::text;

-- An order carried the discount. A discount is redeemed once; a replay of the
-- same order changes nothing.
-- name: RedeemCreditDiscount :execrows
UPDATE credit_discounts
SET state = 'redeemed', provider_invoice_id = sqlc.arg(provider_invoice_id)::text,
    redeemed_cents = sqlc.arg(redeemed_cents)::bigint, updated_at = now()
WHERE id = sqlc.arg(id)::uuid AND state <> 'redeemed';

-- name: DetachCreditDiscount :execrows
UPDATE credit_discounts SET state = 'detached', updated_at = now()
WHERE id = sqlc.arg(id)::uuid AND state = 'attached';

-- Subjects whose discount may need attaching, replacing or removing: those
-- with credit left, and those with a discount still attached.
-- name: CreditDiscountSubjects :many
SELECT s.subject_id::text AS subject_id FROM (
  SELECT c.account_id AS subject_id FROM account_credits c
  WHERE c.cents_remaining > 0 AND (c.expires_at IS NULL OR c.expires_at > now())
  UNION
  SELECT d.subject_id FROM credit_discounts d
  WHERE d.provider = sqlc.arg(provider)::text AND d.state = 'attached'
) s
ORDER BY s.subject_id;
