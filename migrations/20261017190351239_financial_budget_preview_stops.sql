-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
-- ADR-566: independent, latched monitored decisions. Customer policy mutations
-- invalidate their own revision only; they never clear other lifecycle holds.
CREATE TABLE IF NOT EXISTS financial_budget_stops (
  id uuid PRIMARY KEY,
  account_id uuid NOT NULL,
  policy_id uuid NOT NULL,
  revision bigint NOT NULL CHECK (revision BETWEEN 1 AND 9007199254740991),
  period_start timestamptz NOT NULL CHECK (period_start = date_trunc('month', period_start AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'),
  period_end timestamptz NOT NULL CHECK (period_end = (period_start AT TIME ZONE 'UTC' + interval '1 month') AT TIME ZONE 'UTC'),
  requested_at timestamptz NOT NULL CHECK (requested_at >= period_start AND requested_at < period_end),
  drain_deadline timestamptz NOT NULL CHECK (drain_deadline >= requested_at AND drain_deadline <= requested_at + interval '300 seconds'),
  resume_rule text NOT NULL CHECK (resume_rule IN ('manual', 'next_period')),
  payload jsonb NOT NULL CHECK (jsonb_typeof(payload) = 'object'),
  UNIQUE (account_id, policy_id, revision, period_start),
  FOREIGN KEY (account_id, policy_id, revision) REFERENCES financial_budget_revisions(account_id, policy_id, revision) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS financial_budget_stops_account_idx ON financial_budget_stops(account_id, policy_id, revision);

CREATE TABLE IF NOT EXISTS financial_budget_stop_targets (
  stop_id uuid NOT NULL REFERENCES financial_budget_stops(id) ON DELETE CASCADE,
  app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
  environment_id text NOT NULL DEFAULT '' CHECK (environment_id = '' OR environment_id ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'),
  state text NOT NULL CHECK (state IN ('requested', 'draining', 'enforced', 'failed')),
  observed_at timestamptz NOT NULL,
  resident_count integer NOT NULL CHECK (resident_count >= 0 AND (state != 'enforced' OR resident_count = 0)),
  last_error text NOT NULL DEFAULT '' CHECK (last_error IN ('', 'runtime_identity_unavailable', 'vm_teardown_failed', 'instance_settlement_failed')),
  PRIMARY KEY (stop_id, app_id, environment_id)
);
-- +goose StatementEnd

-- +goose Down
DROP TABLE financial_budget_stop_targets;
DROP TABLE financial_budget_stops;

-- migration-compat: contract
-- Financial funding rollout coordinates scheduler, native-owner and meter
-- capabilities with the USD gate; this batch requires a full release.
