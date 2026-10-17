-- filename: 20261014215547104_volume_rate_card_tiers.sql

-- ADR-1309: a rate card's ladder prices a month graduated (each unit at the
-- step its position falls in) or by volume (every unit at the step the
-- month's total reaches). App cards, tenant cards and tenant plans carry the
-- mode; only a card with tiers can be volume.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE api_consumer_rate_cards ADD COLUMN IF NOT EXISTS tier_mode text NOT NULL DEFAULT 'graduated';
DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'api_consumer_rate_cards_tier_mode_chk') THEN
    ALTER TABLE api_consumer_rate_cards ADD CONSTRAINT api_consumer_rate_cards_tier_mode_chk
      CHECK (tier_mode = 'graduated' OR (tier_mode = 'volume' AND jsonb_array_length(tiers) > 0));
  END IF;
END $$;

ALTER TABLE platform_tenant_rate_cards ADD COLUMN IF NOT EXISTS tier_mode text NOT NULL DEFAULT 'graduated';
DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'platform_tenant_rate_cards_tier_mode_chk') THEN
    ALTER TABLE platform_tenant_rate_cards ADD CONSTRAINT platform_tenant_rate_cards_tier_mode_chk
      CHECK (tier_mode = 'graduated' OR (tier_mode = 'volume' AND jsonb_array_length(tiers) > 0));
  END IF;
END $$;

ALTER TABLE platform_tenant_plans ADD COLUMN IF NOT EXISTS tier_mode text NOT NULL DEFAULT 'graduated';
DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'platform_tenant_plans_tier_mode_chk') THEN
    ALTER TABLE platform_tenant_plans ADD CONSTRAINT platform_tenant_plans_tier_mode_chk
      CHECK (tier_mode = 'graduated' OR (tier_mode = 'volume' AND jsonb_array_length(tiers) > 0));
  END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE platform_tenant_plans DROP CONSTRAINT IF EXISTS platform_tenant_plans_tier_mode_chk;
ALTER TABLE platform_tenant_plans DROP COLUMN IF EXISTS tier_mode;
ALTER TABLE platform_tenant_rate_cards DROP CONSTRAINT IF EXISTS platform_tenant_rate_cards_tier_mode_chk;
ALTER TABLE platform_tenant_rate_cards DROP COLUMN IF EXISTS tier_mode;
ALTER TABLE api_consumer_rate_cards DROP CONSTRAINT IF EXISTS api_consumer_rate_cards_tier_mode_chk;
ALTER TABLE api_consumer_rate_cards DROP COLUMN IF EXISTS tier_mode;
-- +goose StatementEnd
