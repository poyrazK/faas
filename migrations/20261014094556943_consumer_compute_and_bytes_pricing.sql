-- filename: 20261014094556943_consumer_compute_and_bytes_pricing.sql

-- ADR-1308: consumer usage minutes sum billable requests' compute time and
-- response size, and app consumer rate cards can price either.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE api_consumer_usage_minutes ADD COLUMN IF NOT EXISTS compute_ms bigint NOT NULL DEFAULT 0;
ALTER TABLE api_consumer_usage_minutes ADD COLUMN IF NOT EXISTS response_bytes bigint NOT NULL DEFAULT 0;
DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'api_consumer_usage_minutes_resources_chk') THEN
    ALTER TABLE api_consumer_usage_minutes ADD CONSTRAINT api_consumer_usage_minutes_resources_chk
      CHECK (compute_ms >= 0 AND response_bytes >= 0);
  END IF;
END $$;

ALTER TABLE api_consumer_rate_cards DROP CONSTRAINT IF EXISTS api_consumer_rate_cards_unit_chk;
ALTER TABLE api_consumer_rate_cards ADD CONSTRAINT api_consumer_rate_cards_unit_chk
  CHECK (unit IN ('request', 'compute_second', 'response_megabyte'));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE api_consumer_rate_cards DROP CONSTRAINT IF EXISTS api_consumer_rate_cards_unit_chk;
ALTER TABLE api_consumer_rate_cards ADD CONSTRAINT api_consumer_rate_cards_unit_chk CHECK (unit = 'request');
ALTER TABLE api_consumer_usage_minutes DROP CONSTRAINT IF EXISTS api_consumer_usage_minutes_resources_chk;
ALTER TABLE api_consumer_usage_minutes DROP COLUMN IF EXISTS response_bytes;
ALTER TABLE api_consumer_usage_minutes DROP COLUMN IF EXISTS compute_ms;
-- +goose StatementEnd
