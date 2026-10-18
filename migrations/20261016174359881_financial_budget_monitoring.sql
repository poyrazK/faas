-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
-- ADR-566: observation and outbox insertion commit together. A policy revision
-- is the alert generation; edits re-evaluate existing monthly spending.
CREATE TABLE IF NOT EXISTS financial_budget_evaluations (
  account_id uuid NOT NULL,
  policy_id uuid NOT NULL,
  revision bigint NOT NULL CHECK (revision BETWEEN 1 AND 9007199254740991),
  period_start timestamptz NOT NULL CHECK (period_start = date_trunc('month', period_start AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'),
  evaluated_at timestamptz NOT NULL CHECK (evaluated_at >= period_start),
  evidence_through_id bigint NOT NULL CHECK (evidence_through_id >= 0),
  observation jsonb NOT NULL CHECK (jsonb_typeof(observation) = 'object'),
  PRIMARY KEY (account_id, policy_id),
  FOREIGN KEY (account_id, policy_id, revision) REFERENCES financial_budget_revisions(account_id, policy_id, revision) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS financial_budget_alerts (
  id uuid PRIMARY KEY,
  account_id uuid NOT NULL,
  policy_id uuid NOT NULL,
  revision bigint NOT NULL CHECK (revision BETWEEN 1 AND 9007199254740991),
  period_start timestamptz NOT NULL CHECK (period_start = date_trunc('month', period_start AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'),
  threshold_millicents bigint NOT NULL CHECK (threshold_millicents BETWEEN 0 AND 9007199254740991),
  payload jsonb NOT NULL CHECK (jsonb_typeof(payload) = 'object'),
  state text NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'sending', 'retrying', 'delivered')),
  attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
  next_attempt_at timestamptz NOT NULL CHECK (next_attempt_at >= period_start),
  lease_token uuid CHECK ((state = 'sending') = (lease_token IS NOT NULL)),
  delivered_at timestamptz CHECK ((state = 'delivered') = (delivered_at IS NOT NULL)),
  last_error text NOT NULL DEFAULT '' CHECK (last_error IN ('', 'mail_delivery_failed')),
  UNIQUE (account_id, policy_id, revision, period_start, threshold_millicents),
  FOREIGN KEY (account_id, policy_id, revision) REFERENCES financial_budget_revisions(account_id, policy_id, revision) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS financial_budget_alert_due_idx ON financial_budget_alerts(next_attempt_at, id) WHERE state != 'delivered';
-- +goose StatementEnd

-- +goose Down
DROP TABLE financial_budget_alerts;
DROP TABLE financial_budget_evaluations;

-- migration-compat: contract
-- Financial funding rollout coordinates scheduler, native-owner and meter
-- capabilities with the USD gate; this batch requires a full release.
