-- filename: 20261017121525216_financial_budget_notifications.sql

-- +goose Up
-- +goose StatementBegin
-- ADR-1500: one durable row per budget, UTC month and crossed threshold.
-- The primary key makes a threshold notify at most once a month; meterd
-- inserts the row before sending and records the delivery outcome on it.
CREATE TABLE IF NOT EXISTS financial_budget_notifications (
  account_id uuid NOT NULL,
  policy_id uuid NOT NULL,
  period_start timestamptz NOT NULL CHECK (
    period_start = date_trunc('month', period_start AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'
  ),
  threshold_millicents bigint NOT NULL CHECK (threshold_millicents BETWEEN 0 AND 9007199254740991),
  policy_revision bigint NOT NULL CHECK (policy_revision BETWEEN 1 AND 9007199254740991),
  known_millicents bigint NOT NULL CHECK (known_millicents BETWEEN threshold_millicents AND 9007199254740991),
  coverage_complete boolean NOT NULL,
  status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'sent', 'abandoned', 'cancelled')),
  attempts integer NOT NULL DEFAULT 0 CHECK (attempts BETWEEN 0 AND 1000),
  last_error text NOT NULL DEFAULT '' CHECK (octet_length(last_error) <= 512),
  claimed_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  finished_at timestamptz CHECK ((status = 'pending') = (finished_at IS NULL)),
  PRIMARY KEY (policy_id, period_start, threshold_millicents),
  FOREIGN KEY (account_id, policy_id) REFERENCES financial_budget_policies(account_id, id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS financial_budget_notifications_pending_idx
  ON financial_budget_notifications (claimed_at) WHERE status = 'pending';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS financial_budget_notifications;
-- +goose StatementEnd
