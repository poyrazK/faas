-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE api_consumer_rate_cards
  ADD COLUMN IF NOT EXISTS model_prices jsonb NOT NULL DEFAULT '{}'::jsonb;

-- +goose Down
SET LOCAL lock_timeout = '5s';
ALTER TABLE api_consumer_rate_cards DROP COLUMN IF EXISTS model_prices;
