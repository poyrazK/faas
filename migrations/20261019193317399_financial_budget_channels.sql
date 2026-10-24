-- filename: 20261019193317399_financial_budget_channels.sql
-- migration-compat: expand

-- +goose Up
-- The foreign keys lock the referenced tables briefly.
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
-- ADR-20261019193500: the notification channels (ADR-749) a budget's alerts
-- also go to, one projected-overrun alert per policy revision and UTC month,
-- and one durable delivery per alert and channel. apid writes the bindings;
-- meterd records alerts and deliveries and is the only claimer. Deleting a
-- channel or a policy removes its rows.
CREATE TABLE IF NOT EXISTS financial_budget_channels (
  account_id uuid NOT NULL,
  policy_id uuid NOT NULL,
  channel_id uuid NOT NULL REFERENCES notification_channels(id) ON DELETE CASCADE,
  PRIMARY KEY (account_id, policy_id, channel_id),
  FOREIGN KEY (account_id, policy_id) REFERENCES financial_budget_policies(account_id, id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS financial_budget_channels_channel_idx ON financial_budget_channels(channel_id);

-- A projection alert has its own account-email outbox, shaped like
-- financial_budget_alerts. Keeping it separate leaves that table's
-- (policy, revision, month, threshold) key and the previous release's inserts
-- unchanged: a projection at the limit never displaces the limit alert.
CREATE TABLE IF NOT EXISTS financial_budget_projection_alerts (
  id uuid PRIMARY KEY,
  account_id uuid NOT NULL,
  policy_id uuid NOT NULL,
  revision bigint NOT NULL CHECK (revision BETWEEN 1 AND 9007199254740991),
  period_start timestamptz NOT NULL CHECK (period_start = date_trunc('month', period_start AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'),
  limit_millicents bigint NOT NULL CHECK (limit_millicents BETWEEN 1 AND 9007199254740991),
  payload jsonb NOT NULL CHECK (jsonb_typeof(payload) = 'object'),
  state text NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'sending', 'retrying', 'delivered')),
  attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
  next_attempt_at timestamptz NOT NULL CHECK (next_attempt_at >= period_start),
  lease_token uuid CHECK ((state = 'sending') = (lease_token IS NOT NULL)),
  delivered_at timestamptz CHECK ((state = 'delivered') = (delivered_at IS NOT NULL)),
  last_error text NOT NULL DEFAULT '' CHECK (last_error IN ('', 'mail_delivery_failed')),
  UNIQUE (account_id, policy_id, revision, period_start),
  FOREIGN KEY (account_id, policy_id, revision) REFERENCES financial_budget_revisions(account_id, policy_id, revision) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS financial_budget_projection_alert_due_idx ON financial_budget_projection_alerts(next_attempt_at, id) WHERE state != 'delivered';

-- alert_id is a financial_budget_alerts id (kind threshold) or a
-- financial_budget_projection_alerts id (kind projection); both are derived
-- from distinct keys, so they never collide.
CREATE TABLE IF NOT EXISTS financial_budget_alert_channel_deliveries (
  alert_id uuid NOT NULL,
  kind text NOT NULL CHECK (kind IN ('threshold', 'projection')),
  channel_id uuid NOT NULL REFERENCES notification_channels(id) ON DELETE CASCADE,
  account_id uuid NOT NULL,
  policy_id uuid NOT NULL,
  state text NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'sending', 'retrying', 'delivered', 'abandoned')),
  attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
  next_attempt_at timestamptz NOT NULL,
  lease_token uuid CHECK ((state = 'sending') = (lease_token IS NOT NULL)),
  delivered_at timestamptz CHECK ((state = 'delivered') = (delivered_at IS NOT NULL)),
  last_error text NOT NULL DEFAULT '' CHECK (last_error IN ('', 'channel_delivery_failed', 'channel_unavailable')),
  PRIMARY KEY (alert_id, channel_id),
  FOREIGN KEY (account_id, policy_id) REFERENCES financial_budget_policies(account_id, id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS financial_budget_alert_channel_due_idx ON financial_budget_alert_channel_deliveries(next_attempt_at, alert_id, channel_id)
  WHERE state IN ('pending', 'sending', 'retrying');
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS financial_budget_alert_channel_deliveries;
DROP TABLE IF EXISTS financial_budget_projection_alerts;
DROP TABLE IF EXISTS financial_budget_channels;
-- +goose StatementEnd
