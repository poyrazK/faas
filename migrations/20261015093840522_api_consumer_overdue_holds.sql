-- filename: 20261015093840522_api_consumer_overdue_holds.sql

-- ADR-1315: an app can block consumers whose exported invoice is still
-- unpaid a number of days after its due date. apid recomputes the holds;
-- the gateway reads a consumer's hold with its plan policy.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS api_consumer_billing_policies (
    app_id uuid PRIMARY KEY REFERENCES apps(id) ON DELETE CASCADE,
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    block_overdue_after_days integer NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT api_consumer_billing_policies_days_chk CHECK (block_overdue_after_days BETWEEN 0 AND 365)
);
COMMENT ON TABLE api_consumer_billing_policies IS
  'Per-app rule blocking API consumers whose exported invoice is unpaid that many days after its due date (ADR-1315).';

CREATE TABLE IF NOT EXISTS api_consumer_overdue_holds (
    consumer_id uuid PRIMARY KEY REFERENCES api_consumers(id) ON DELETE CASCADE,
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    app_id uuid NOT NULL,
    export_id uuid NOT NULL REFERENCES usage_statement_invoice_exports(id) ON DELETE CASCADE,
    due_at timestamptz NOT NULL,
    since timestamptz NOT NULL
);
CREATE INDEX IF NOT EXISTS api_consumer_overdue_holds_app_idx ON api_consumer_overdue_holds (account_id, app_id, due_at);
COMMENT ON TABLE api_consumer_overdue_holds IS
  'API consumers the gateway refuses because of an overdue invoice; recomputed by apid (ADR-1315).';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS api_consumer_overdue_holds;
DROP TABLE IF EXISTS api_consumer_billing_policies;
-- +goose StatementEnd
