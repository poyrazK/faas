-- filename: 20260925210000001_platform_tenant_rate_cards.sql

-- +goose Up
-- +goose StatementBegin
-- Immutable, effective-dated customer tariffs shared across a platform
-- tenant's apps. A later card changes only how future usage minutes are priced.
CREATE TABLE IF NOT EXISTS platform_tenant_rate_cards (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  account_id uuid NOT NULL,
  platform_tenant_id uuid NOT NULL,
  currency text NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
  unit text NOT NULL CHECK (unit = 'request'),
  price_millicents_per_unit bigint NOT NULL CHECK (price_millicents_per_unit >= 0),
  effective_from timestamptz NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (account_id, platform_tenant_id, effective_from),
  FOREIGN KEY (account_id, platform_tenant_id)
    REFERENCES platform_tenants(account_id, id) ON DELETE CASCADE,
  CHECK (effective_from = date_trunc('minute', effective_from AT TIME ZONE 'UTC') AT TIME ZONE 'UTC')
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS platform_tenant_rate_cards;
-- +goose StatementEnd
