-- filename: 20261017143754793_platform_tenant_unit_streams_and_meters.sql

-- ADR-1319: a platform tenant's cards of different units price side by side
-- (as app cards do, ADR-1317), so effective_from is unique per unit, and
-- tenant cards and plans can price units the app reports (ADR-1318).
-- Re-runnable: constraints are dropped before being re-added or guarded.

-- migration-compat: contract
-- Contract: it replaces the per-minute unique key with a per-unit one, and
-- the previous apid relied on the old key to refuse a second card in a minute.
-- migration-lint: allow index-build platform_tenant_rate_cards: API monetization is unreleased (ADR-20261018090210), so the table holds a handful of rows and the unique index builds in milliseconds

-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
ALTER TABLE platform_tenant_rate_cards DROP CONSTRAINT IF EXISTS platform_tenant_rate_cards_account_id_platform_tenant_id_ef_key;
DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'platform_tenant_rate_cards_unit_effective_key' AND conrelid = 'platform_tenant_rate_cards'::regclass) THEN
    ALTER TABLE platform_tenant_rate_cards ADD CONSTRAINT platform_tenant_rate_cards_unit_effective_key
      UNIQUE (account_id, platform_tenant_id, unit, effective_from);
  END IF;
END $$;
ALTER TABLE platform_tenant_rate_cards DROP CONSTRAINT IF EXISTS platform_tenant_rate_cards_unit_check;
ALTER TABLE platform_tenant_rate_cards ADD CONSTRAINT platform_tenant_rate_cards_unit_check
  CHECK (unit IN ('request', 'compute_second', 'response_megabyte') OR unit ~ '^app:[a-z][a-z0-9_]{0,31}$');
ALTER TABLE platform_tenant_plans DROP CONSTRAINT IF EXISTS platform_tenant_plans_unit_chk;
ALTER TABLE platform_tenant_plans ADD CONSTRAINT platform_tenant_plans_unit_chk
  CHECK (unit IN ('request', 'compute_second', 'response_megabyte') OR unit ~ '^app:[a-z][a-z0-9_]{0,31}$');
CREATE TABLE IF NOT EXISTS platform_tenant_meter_usage_minutes (
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    platform_tenant_id uuid NOT NULL,
    app_id uuid NOT NULL,
    source_kind text NOT NULL,
    subject_key text NOT NULL,
    meter text NOT NULL,
    window_start timestamptz NOT NULL,
    units bigint NOT NULL DEFAULT 0,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (account_id, platform_tenant_id, app_id, source_kind, subject_key, meter, window_start),
    CONSTRAINT platform_tenant_meter_usage_minutes_kind_chk CHECK (source_kind IN ('consumer', 'surface', 'jwt')),
    CONSTRAINT platform_tenant_meter_usage_minutes_subject_chk
      CHECK (subject_key ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'),
    CONSTRAINT platform_tenant_meter_usage_minutes_meter_chk CHECK (meter ~ '^[a-z][a-z0-9_]{0,31}$'),
    CONSTRAINT platform_tenant_meter_usage_minutes_minute_chk
      CHECK (window_start = date_trunc('minute', window_start AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'),
    CONSTRAINT platform_tenant_meter_usage_minutes_units_chk CHECK (units >= 0)
);
CREATE INDEX IF NOT EXISTS platform_tenant_meter_usage_minutes_read_idx
  ON platform_tenant_meter_usage_minutes (account_id, platform_tenant_id, meter, window_start);
COMMENT ON TABLE platform_tenant_meter_usage_minutes IS
  'Units an app reported per platform tenant subject, meter and UTC minute for billable requests (ADR-1319).';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS platform_tenant_meter_usage_minutes;
ALTER TABLE platform_tenant_plans DROP CONSTRAINT IF EXISTS platform_tenant_plans_unit_chk;
ALTER TABLE platform_tenant_plans ADD CONSTRAINT platform_tenant_plans_unit_chk
  CHECK (unit IN ('request', 'compute_second', 'response_megabyte'));
ALTER TABLE platform_tenant_rate_cards DROP CONSTRAINT IF EXISTS platform_tenant_rate_cards_unit_check;
ALTER TABLE platform_tenant_rate_cards ADD CONSTRAINT platform_tenant_rate_cards_unit_check
  CHECK (unit IN ('request', 'compute_second', 'response_megabyte'));
ALTER TABLE platform_tenant_rate_cards DROP CONSTRAINT IF EXISTS platform_tenant_rate_cards_unit_effective_key;
ALTER TABLE platform_tenant_rate_cards ADD CONSTRAINT platform_tenant_rate_cards_account_id_platform_tenant_id_ef_key
  UNIQUE (account_id, platform_tenant_id, effective_from);
-- +goose StatementEnd
