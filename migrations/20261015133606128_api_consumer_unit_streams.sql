-- filename: 20261015133606128_api_consumer_unit_streams.sql

-- ADR-1317: app consumer rate cards of different units are parallel price
-- histories, so one minute can start a card per unit.

-- +goose Up
-- +goose StatementBegin
DROP INDEX IF EXISTS api_consumer_rate_cards_default_effective_uniq;
DROP INDEX IF EXISTS api_consumer_rate_cards_plan_effective_uniq;
CREATE UNIQUE INDEX IF NOT EXISTS api_consumer_rate_cards_default_unit_effective_uniq
  ON api_consumer_rate_cards (app_id, unit, effective_from) WHERE plan_id IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS api_consumer_rate_cards_plan_unit_effective_uniq
  ON api_consumer_rate_cards (plan_id, unit, effective_from) WHERE plan_id IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS api_consumer_rate_cards_plan_unit_effective_uniq;
DROP INDEX IF EXISTS api_consumer_rate_cards_default_unit_effective_uniq;
CREATE UNIQUE INDEX IF NOT EXISTS api_consumer_rate_cards_default_effective_uniq
  ON api_consumer_rate_cards (app_id, effective_from) WHERE plan_id IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS api_consumer_rate_cards_plan_effective_uniq
  ON api_consumer_rate_cards (plan_id, effective_from) WHERE plan_id IS NOT NULL;
-- +goose StatementEnd
