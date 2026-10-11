-- filename: 20261009150457273_api_consumer_rate_card_tiers.sql

-- ADR-845: an app rate card can carry a graduated tier ladder. An empty
-- ladder keeps the single price and optional monthly allowance (ADR-844), so
-- existing cards and statements are unchanged.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE api_consumer_rate_cards
  ADD COLUMN IF NOT EXISTS tiers jsonb NOT NULL DEFAULT '[]'::jsonb;

ALTER TABLE api_consumer_rate_cards
  DROP CONSTRAINT IF EXISTS api_consumer_rate_cards_tiers_chk;
ALTER TABLE api_consumer_rate_cards
  ADD CONSTRAINT api_consumer_rate_cards_tiers_chk
    CHECK (jsonb_typeof(tiers) = 'array' AND jsonb_array_length(tiers) <= 10);

COMMENT ON COLUMN api_consumer_rate_cards.tiers IS
  'Graduated price ladder [{up_to, price_millicents_per_unit}], counted per consumer per UTC calendar month in minute order; empty means the single price and allowance apply.';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE api_consumer_rate_cards
  DROP CONSTRAINT IF EXISTS api_consumer_rate_cards_tiers_chk;
ALTER TABLE api_consumer_rate_cards
  DROP COLUMN IF EXISTS tiers;
-- +goose StatementEnd
