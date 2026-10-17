-- filename: 20261015122215439_api_consumer_monthly_fees.sql

-- ADR-1316: app consumer rate cards can charge a monthly base fee and a
-- minimum monthly spend per consumer; statements carry the fees they bill.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE api_consumer_rate_cards ADD COLUMN IF NOT EXISTS base_fee_millicents_per_month bigint NOT NULL DEFAULT 0;
ALTER TABLE api_consumer_rate_cards ADD COLUMN IF NOT EXISTS minimum_millicents_per_month bigint NOT NULL DEFAULT 0;
DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'api_consumer_rate_cards_monthly_fees_chk') THEN
    ALTER TABLE api_consumer_rate_cards ADD CONSTRAINT api_consumer_rate_cards_monthly_fees_chk
      CHECK (base_fee_millicents_per_month >= 0 AND minimum_millicents_per_month >= 0);
  END IF;
END $$;

ALTER TABLE api_consumer_usage_statements ADD COLUMN IF NOT EXISTS fees jsonb NOT NULL DEFAULT '[]'::jsonb;
DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'api_consumer_usage_statements_fees_chk') THEN
    ALTER TABLE api_consumer_usage_statements ADD CONSTRAINT api_consumer_usage_statements_fees_chk
      CHECK (jsonb_typeof(fees) = 'array' AND jsonb_array_length(fees) <= 64);
  END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE api_consumer_usage_statements DROP CONSTRAINT IF EXISTS api_consumer_usage_statements_fees_chk;
ALTER TABLE api_consumer_usage_statements DROP COLUMN IF EXISTS fees;
ALTER TABLE api_consumer_rate_cards DROP CONSTRAINT IF EXISTS api_consumer_rate_cards_monthly_fees_chk;
ALTER TABLE api_consumer_rate_cards DROP COLUMN IF EXISTS minimum_millicents_per_month;
ALTER TABLE api_consumer_rate_cards DROP COLUMN IF EXISTS base_fee_millicents_per_month;
-- +goose StatementEnd
