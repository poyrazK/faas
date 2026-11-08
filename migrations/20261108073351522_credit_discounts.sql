-- migration-compat: expand
-- filename: 20261108073351522_credit_discounts.sql

-- ADR-20261104212028: account credit reaches the customer as a one-time fixed
-- discount on the billing provider subscription's next order, instead of a
-- refund after the card is charged. Each row is one discount Gregale created
-- and attached for a billing subject (an account or a company organization).
-- An order that carries the discount draws the amount the provider applied
-- from the subject's credits, once (credit_ledger keeps the debit). apid is
-- the only writer; the rows go with the subject's resource namespace.

-- +goose Up
SET LOCAL lock_timeout = '5s';

CREATE TABLE IF NOT EXISTS credit_discounts (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    subject_id uuid NOT NULL REFERENCES resource_namespaces(id) ON DELETE CASCADE,
    provider text NOT NULL CHECK (provider IN ('polar')),
    provider_subscription_id text NOT NULL CHECK (char_length(provider_subscription_id) BETWEEN 1 AND 200),
    provider_discount_id text NOT NULL CHECK (char_length(provider_discount_id) BETWEEN 1 AND 200),
    amount_cents bigint NOT NULL CHECK (amount_cents > 0),
    currency text NOT NULL CHECK (currency ~ '^[a-z]{3}$'),
    state text NOT NULL DEFAULT 'attached' CHECK (state IN ('attached', 'redeemed', 'detached')),
    provider_invoice_id text CHECK (provider_invoice_id IS NULL OR char_length(provider_invoice_id) BETWEEN 1 AND 200),
    redeemed_cents bigint CHECK (redeemed_cents IS NULL OR redeemed_cents >= 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT credit_discounts_redeemed_check CHECK (
        (state = 'redeemed') = (provider_invoice_id IS NOT NULL AND redeemed_cents IS NOT NULL))
);
CREATE UNIQUE INDEX IF NOT EXISTS credit_discounts_provider_discount_key
    ON credit_discounts (provider, provider_discount_id);
CREATE INDEX IF NOT EXISTS credit_discounts_attached_subject_idx
    ON credit_discounts (subject_id) WHERE state = 'attached';
COMMENT ON TABLE credit_discounts IS 'One-time fixed provider discounts Gregale attached to a billing subject''s subscription to apply its account credit to the next order; redeemed when an order carries it. Written only by apid (ADR-20261104212028).';

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TABLE IF EXISTS credit_discounts;
