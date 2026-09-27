-- filename: 20260925104915628_platform_tenant_request_budgets.sql

-- +goose Up
-- +goose StatementBegin
-- One bounded policy/counter row per tenant. Admission serializes on this
-- row across gateway replicas; the financial usage ledger remains separate.
CREATE TABLE IF NOT EXISTS platform_tenant_request_budgets (
  tenant_id uuid PRIMARY KEY,
  account_id uuid NOT NULL,
  max_requests_per_minute bigint NOT NULL DEFAULT 0 CHECK (max_requests_per_minute >= 0),
  max_requests_per_day bigint NOT NULL DEFAULT 0 CHECK (max_requests_per_day >= 0),
  minute_start timestamptz,
  minute_used bigint NOT NULL DEFAULT 0 CHECK (minute_used >= 0),
  day_start timestamptz,
  day_used bigint NOT NULL DEFAULT 0 CHECK (day_used >= 0),
  updated_at timestamptz NOT NULL DEFAULT now(),
  FOREIGN KEY (account_id, tenant_id) REFERENCES platform_tenants(account_id, id) ON DELETE CASCADE,
  CHECK (minute_start IS NOT NULL OR minute_used = 0),
  CHECK (day_start IS NOT NULL OR day_used = 0),
  CHECK (minute_start IS NULL OR minute_start = date_trunc('minute', minute_start AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'),
  CHECK (day_start IS NULL OR day_start = date_trunc('day', day_start AT TIME ZONE 'UTC') AT TIME ZONE 'UTC')
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS platform_tenant_request_budgets;
-- +goose StatementEnd
