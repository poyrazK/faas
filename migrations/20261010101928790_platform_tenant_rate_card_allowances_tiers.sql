-- filename: 20261010101928790_platform_tenant_rate_card_allowances_tiers.sql

-- ADR-939: a platform tenant rate card can carry a monthly allowance or a
-- graduated tier ladder, counted across all of the tenant's attributed usage
-- per UTC month. Zero and empty keep the single flat price, so existing cards
-- and statements are unchanged.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE platform_tenant_rate_cards
  ADD COLUMN IF NOT EXISTS included_units_per_month bigint NOT NULL DEFAULT 0;
ALTER TABLE platform_tenant_rate_cards
  ADD COLUMN IF NOT EXISTS tiers jsonb NOT NULL DEFAULT '[]'::jsonb;

ALTER TABLE platform_tenant_rate_cards
  DROP CONSTRAINT IF EXISTS platform_tenant_rate_cards_included_units_chk;
ALTER TABLE platform_tenant_rate_cards
  ADD CONSTRAINT platform_tenant_rate_cards_included_units_chk CHECK (included_units_per_month >= 0);
ALTER TABLE platform_tenant_rate_cards
  DROP CONSTRAINT IF EXISTS platform_tenant_rate_cards_tiers_chk;
ALTER TABLE platform_tenant_rate_cards
  ADD CONSTRAINT platform_tenant_rate_cards_tiers_chk
    CHECK (jsonb_typeof(tiers) = 'array' AND jsonb_array_length(tiers) <= 10
           AND (jsonb_array_length(tiers) = 0 OR included_units_per_month = 0));

COMMENT ON COLUMN platform_tenant_rate_cards.included_units_per_month IS
  'Free request units per tenant per UTC calendar month across all attributed apps, consumed in minute order while this card is effective.';
COMMENT ON COLUMN platform_tenant_rate_cards.tiers IS
  'Graduated price ladder [{up_to, price_millicents_per_unit}], counted per tenant per UTC calendar month across all attributed apps; empty means the single price and allowance apply.';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE platform_tenant_rate_cards
  DROP CONSTRAINT IF EXISTS platform_tenant_rate_cards_tiers_chk;
ALTER TABLE platform_tenant_rate_cards
  DROP CONSTRAINT IF EXISTS platform_tenant_rate_cards_included_units_chk;
ALTER TABLE platform_tenant_rate_cards
  DROP COLUMN IF EXISTS tiers;
ALTER TABLE platform_tenant_rate_cards
  DROP COLUMN IF EXISTS included_units_per_month;
-- +goose StatementEnd
