-- +goose Up
-- +goose StatementBegin

-- Persist the last cumulative host-interface counters that meterd committed to
-- usage_minutes.  Advancing this row and adding the derived delta to the usage
-- row happens in one PostgreSQL transaction, so a meterd restart cannot either
-- forget an acknowledged counter or replay it twice.
CREATE TABLE IF NOT EXISTS meter_network_checkpoints (
  instance_id   uuid PRIMARY KEY REFERENCES instances(id) ON DELETE CASCADE,
  net_tx_bytes  bigint NOT NULL CHECK (net_tx_bytes >= 0),
  net_rx_bytes  bigint NOT NULL CHECK (net_rx_bytes >= 0),
  net_tx_valid  boolean NOT NULL DEFAULT false,
  net_rx_valid  boolean NOT NULL DEFAULT false,
  observed_at   timestamptz NOT NULL,
  updated_at    timestamptz NOT NULL DEFAULT now()
);

-- Keep the compute-only billing_usage_deliveries table unchanged: an older
-- meterd binary explicitly names its three-column conflict key, so replacing
-- that key would break a rolling deployment. New meters use a separate ledger
-- whose durable identity includes the meter.
CREATE TABLE IF NOT EXISTS billing_meter_usage_deliveries (
  provider      text NOT NULL CHECK (provider IN ('stripe', 'paddle', 'polar')),
  account_id    uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  meter         text NOT NULL CHECK (meter IN ('compute', 'egress')),
  window_start  timestamptz NOT NULL,
  quantity      bigint NOT NULL CHECK (quantity >= 0),
  delivered_at  timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (provider, account_id, meter, window_start)
);

CREATE INDEX IF NOT EXISTS billing_meter_usage_deliveries_window_idx
  ON billing_meter_usage_deliveries (window_start);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS meter_network_checkpoints;
DROP TABLE IF EXISTS billing_meter_usage_deliveries;

-- +goose StatementEnd
