-- filename: 20261009141146607_api_consumer_rate_card_allowances.sql

-- ADR-844: an app rate card can include a monthly allowance of free request
-- units per consumer. Existing cards include nothing, so current prices and
-- statements are unchanged.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE api_consumer_rate_cards
  ADD COLUMN IF NOT EXISTS included_units_per_month bigint NOT NULL DEFAULT 0;

ALTER TABLE api_consumer_rate_cards
  DROP CONSTRAINT IF EXISTS api_consumer_rate_cards_included_units_chk;
ALTER TABLE api_consumer_rate_cards
  ADD CONSTRAINT api_consumer_rate_cards_included_units_chk CHECK (included_units_per_month >= 0);

COMMENT ON COLUMN api_consumer_rate_cards.included_units_per_month IS
  'Free request units per consumer per UTC calendar month, consumed in minute order while this card is effective.';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE api_consumer_rate_cards
  DROP CONSTRAINT IF EXISTS api_consumer_rate_cards_included_units_chk;
ALTER TABLE api_consumer_rate_cards
  DROP COLUMN IF EXISTS included_units_per_month;
-- +goose StatementEnd
