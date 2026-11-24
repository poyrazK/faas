-- +goose Up
ALTER TABLE api_consumer_rate_cards
  ADD COLUMN IF NOT EXISTS model_prices jsonb NOT NULL DEFAULT '{}'::jsonb;

-- +goose Down
ALTER TABLE api_consumer_rate_cards DROP COLUMN IF EXISTS model_prices;
