-- filename: 20261014091245143_platform_tenant_route_weights_and_plans.sql

-- ADR-1307: platform tenant rate cards weight routes, and tenant plans
-- bundle card terms with a request budget that assignment applies.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE platform_tenant_rate_cards ADD COLUMN IF NOT EXISTS route_weights jsonb NOT NULL DEFAULT '{}'::jsonb;
DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'platform_tenant_rate_cards_route_weights_chk') THEN
    ALTER TABLE platform_tenant_rate_cards ADD CONSTRAINT platform_tenant_rate_cards_route_weights_chk
      CHECK (jsonb_typeof(route_weights) = 'object');
  END IF;
END $$;
COMMENT ON COLUMN platform_tenant_rate_cards.route_weights IS 'Units charged per consumer request on a route label ("METHOD /template" -> weight); unlisted routes and non-consumer subjects count 1.';

CREATE TABLE IF NOT EXISTS platform_tenant_plans (
  id                        uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  account_id                uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  name                      text NOT NULL,
  currency                  text NOT NULL,
  price_millicents_per_unit bigint NOT NULL,
  included_units_per_month  bigint NOT NULL DEFAULT 0,
  tiers                     jsonb NOT NULL DEFAULT '[]'::jsonb,
  route_weights             jsonb NOT NULL DEFAULT '{}'::jsonb,
  max_requests_per_minute   bigint,
  max_requests_per_day      bigint,
  created_at                timestamptz NOT NULL DEFAULT now(),
  updated_at                timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT platform_tenant_plans_name_chk CHECK (name ~ '^[a-z0-9][a-z0-9-]{0,62}$'),
  CONSTRAINT platform_tenant_plans_currency_chk CHECK (currency ~ '^[A-Z]{3}$'),
  CONSTRAINT platform_tenant_plans_price_chk CHECK (price_millicents_per_unit >= 0 AND included_units_per_month >= 0),
  CONSTRAINT platform_tenant_plans_tiers_chk CHECK (jsonb_typeof(tiers) = 'array' AND jsonb_array_length(tiers) <= 10
    AND (jsonb_array_length(tiers) = 0 OR included_units_per_month = 0)),
  CONSTRAINT platform_tenant_plans_route_weights_chk CHECK (jsonb_typeof(route_weights) = 'object'),
  CONSTRAINT platform_tenant_plans_budget_chk CHECK ((max_requests_per_minute IS NULL) = (max_requests_per_day IS NULL)
    AND (max_requests_per_minute IS NULL OR (max_requests_per_minute >= 0 AND max_requests_per_day >= 0))),
  CONSTRAINT platform_tenant_plans_name_key UNIQUE (account_id, name)
);
COMMENT ON TABLE platform_tenant_plans IS 'Reusable platform tenant price and request-budget bundle; assigning one creates the tenant''s rate card and sets its budget (ADR-1307).';

CREATE TABLE IF NOT EXISTS platform_tenant_plan_assignments (
  id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  account_id     uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  tenant_id      uuid NOT NULL REFERENCES platform_tenants(id) ON DELETE CASCADE,
  plan_id        uuid NOT NULL REFERENCES platform_tenant_plans(id) ON DELETE CASCADE,
  rate_card_id   uuid NOT NULL REFERENCES platform_tenant_rate_cards(id) ON DELETE CASCADE,
  effective_from timestamptz NOT NULL,
  created_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS platform_tenant_plan_assignments_tenant_idx
  ON platform_tenant_plan_assignments (tenant_id, effective_from DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS platform_tenant_plan_assignments;
DROP TABLE IF EXISTS platform_tenant_plans;
ALTER TABLE platform_tenant_rate_cards DROP CONSTRAINT IF EXISTS platform_tenant_rate_cards_route_weights_chk;
ALTER TABLE platform_tenant_rate_cards DROP COLUMN IF EXISTS route_weights;
-- +goose StatementEnd
