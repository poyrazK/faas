-- filename: 20261019193317399_financial_budget_channels.sql
-- migration-compat: expand

-- +goose Up
-- The foreign keys lock the referenced tables briefly.
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
-- ADR-20261019193500: the notification channels (ADR-749) a budget's
-- threshold alerts also go to, and one durable delivery per alert and
-- channel. apid writes the bindings; meterd enqueues a delivery with each
-- alert it records and is the only claimer. Deleting a channel, a policy or
-- an alert removes its rows.
CREATE TABLE IF NOT EXISTS financial_budget_channels (
  account_id uuid NOT NULL,
  policy_id uuid NOT NULL,
  channel_id uuid NOT NULL REFERENCES notification_channels(id) ON DELETE CASCADE,
  PRIMARY KEY (account_id, policy_id, channel_id),
  FOREIGN KEY (account_id, policy_id) REFERENCES financial_budget_policies(account_id, id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS financial_budget_channels_channel_idx ON financial_budget_channels(channel_id);

CREATE TABLE IF NOT EXISTS financial_budget_alert_channel_deliveries (
  alert_id uuid NOT NULL REFERENCES financial_budget_alerts(id) ON DELETE CASCADE,
  channel_id uuid NOT NULL REFERENCES notification_channels(id) ON DELETE CASCADE,
  account_id uuid NOT NULL,
  state text NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'sending', 'retrying', 'delivered', 'abandoned')),
  attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
  next_attempt_at timestamptz NOT NULL,
  lease_token uuid CHECK ((state = 'sending') = (lease_token IS NOT NULL)),
  delivered_at timestamptz CHECK ((state = 'delivered') = (delivered_at IS NOT NULL)),
  last_error text NOT NULL DEFAULT '' CHECK (last_error IN ('', 'channel_delivery_failed', 'channel_unavailable')),
  PRIMARY KEY (alert_id, channel_id)
);
CREATE INDEX IF NOT EXISTS financial_budget_alert_channel_due_idx ON financial_budget_alert_channel_deliveries(next_attempt_at, alert_id, channel_id)
  WHERE state IN ('pending', 'sending', 'retrying');
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS financial_budget_alert_channel_deliveries;
DROP TABLE IF EXISTS financial_budget_channels;
-- +goose StatementEnd
