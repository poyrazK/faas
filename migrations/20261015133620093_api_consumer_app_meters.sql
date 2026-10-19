-- filename: 20261015133620093_api_consumer_app_meters.sql

-- ADR-1318: apps report billable units per request (tokens, images) in a
-- Gregale-Usage header or trailer; apid keeps them per consumer, meter and
-- minute, and cards with unit "app:<meter>" price them.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE api_consumer_rate_cards DROP CONSTRAINT IF EXISTS api_consumer_rate_cards_unit_chk;
ALTER TABLE api_consumer_rate_cards ADD CONSTRAINT api_consumer_rate_cards_unit_chk
  CHECK (unit IN ('request', 'compute_second', 'response_megabyte') OR unit ~ '^app:[a-z][a-z0-9_]{0,31}$');

CREATE TABLE IF NOT EXISTS api_consumer_meter_usage_minutes (
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    app_id uuid NOT NULL,
    consumer_key text NOT NULL,
    meter text NOT NULL,
    window_start timestamptz NOT NULL,
    units bigint NOT NULL DEFAULT 0,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (account_id, app_id, consumer_key, meter, window_start),
    CONSTRAINT api_consumer_meter_usage_minutes_consumer_key_chk
      CHECK (consumer_key ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'),
    CONSTRAINT api_consumer_meter_usage_minutes_meter_chk CHECK (meter ~ '^[a-z][a-z0-9_]{0,31}$'),
    CONSTRAINT api_consumer_meter_usage_minutes_minute_chk
      CHECK (window_start = date_trunc('minute', window_start AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'),
    CONSTRAINT api_consumer_meter_usage_minutes_units_chk CHECK (units >= 0)
);
COMMENT ON TABLE api_consumer_meter_usage_minutes IS
  'Units an app reported per consumer, meter and UTC minute for billable requests (ADR-1318).';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS api_consumer_meter_usage_minutes;
ALTER TABLE api_consumer_rate_cards DROP CONSTRAINT IF EXISTS api_consumer_rate_cards_unit_chk;
ALTER TABLE api_consumer_rate_cards ADD CONSTRAINT api_consumer_rate_cards_unit_chk
  CHECK (unit IN ('request', 'compute_second', 'response_megabyte'));
-- +goose StatementEnd
