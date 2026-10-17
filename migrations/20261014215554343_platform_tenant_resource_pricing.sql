-- filename: 20261014215554343_platform_tenant_resource_pricing.sql

-- ADR-1310: tenant usage minutes sum billable requests' compute time and
-- response size, tenant cards and plans can price either, and surface and
-- JWT-rule requests keep per-route counts so tenant route weights apply to
-- them.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE platform_tenant_usage_minutes ADD COLUMN IF NOT EXISTS compute_ms bigint NOT NULL DEFAULT 0;
ALTER TABLE platform_tenant_usage_minutes ADD COLUMN IF NOT EXISTS response_bytes bigint NOT NULL DEFAULT 0;
DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'platform_tenant_usage_minutes_resources_chk') THEN
    ALTER TABLE platform_tenant_usage_minutes ADD CONSTRAINT platform_tenant_usage_minutes_resources_chk
      CHECK (compute_ms >= 0 AND response_bytes >= 0);
  END IF;
END $$;

ALTER TABLE platform_tenant_rate_cards DROP CONSTRAINT IF EXISTS platform_tenant_rate_cards_unit_check;
ALTER TABLE platform_tenant_rate_cards ADD CONSTRAINT platform_tenant_rate_cards_unit_check
  CHECK (unit IN ('request', 'compute_second', 'response_megabyte'));

ALTER TABLE platform_tenant_plans ADD COLUMN IF NOT EXISTS unit text NOT NULL DEFAULT 'request';
DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'platform_tenant_plans_unit_chk') THEN
    ALTER TABLE platform_tenant_plans ADD CONSTRAINT platform_tenant_plans_unit_chk
      CHECK (unit IN ('request', 'compute_second', 'response_megabyte'));
  END IF;
END $$;

CREATE TABLE IF NOT EXISTS platform_tenant_source_route_usage_minutes (
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    platform_tenant_id uuid NOT NULL,
    app_id uuid NOT NULL,
    source_kind text NOT NULL,
    source_id uuid NOT NULL,
    route text NOT NULL,
    window_start timestamptz NOT NULL,
    billable_units bigint NOT NULL DEFAULT 0,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (account_id, platform_tenant_id, app_id, source_kind, source_id, window_start, route),
    CONSTRAINT platform_tenant_source_route_usage_minutes_kind_chk CHECK (source_kind IN ('surface', 'jwt')),
    CONSTRAINT platform_tenant_source_route_usage_minutes_route_chk CHECK (char_length(route) BETWEEN 3 AND 256),
    CONSTRAINT platform_tenant_source_route_usage_minutes_minute_chk
      CHECK (window_start = date_trunc('minute', window_start AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'),
    CONSTRAINT platform_tenant_source_route_usage_minutes_units_chk CHECK (billable_units >= 0)
);
CREATE INDEX IF NOT EXISTS platform_tenant_source_route_usage_minutes_read_idx
  ON platform_tenant_source_route_usage_minutes (account_id, platform_tenant_id, window_start);
COMMENT ON TABLE platform_tenant_source_route_usage_minutes IS
  'Billable units per tenant surface or JWT authorization rule, bounded route label, and UTC minute; consumer routes stay in api_consumer_route_usage_minutes.';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS platform_tenant_source_route_usage_minutes;
ALTER TABLE platform_tenant_plans DROP CONSTRAINT IF EXISTS platform_tenant_plans_unit_chk;
ALTER TABLE platform_tenant_plans DROP COLUMN IF EXISTS unit;
ALTER TABLE platform_tenant_rate_cards DROP CONSTRAINT IF EXISTS platform_tenant_rate_cards_unit_check;
ALTER TABLE platform_tenant_rate_cards ADD CONSTRAINT platform_tenant_rate_cards_unit_check CHECK (unit = 'request');
ALTER TABLE platform_tenant_usage_minutes DROP CONSTRAINT IF EXISTS platform_tenant_usage_minutes_resources_chk;
ALTER TABLE platform_tenant_usage_minutes DROP COLUMN IF EXISTS response_bytes;
ALTER TABLE platform_tenant_usage_minutes DROP COLUMN IF EXISTS compute_ms;
-- +goose StatementEnd
