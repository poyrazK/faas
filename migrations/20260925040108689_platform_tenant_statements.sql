-- filename: 20260925040108689_platform_tenant_statements.sql

-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS platform_tenant_statements (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    platform_tenant_id uuid NOT NULL,
    period_start timestamptz NOT NULL,
    period_end timestamptz NOT NULL,
    revision integer NOT NULL CHECK (revision > 0),
    status text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'finalized', 'superseded')),
    currency text NOT NULL DEFAULT '' CHECK (currency = '' OR currency ~ '^[A-Z]{3}$'),
    billable_units bigint NOT NULL CHECK (billable_units >= 0),
    unpriced_units bigint NOT NULL CHECK (unpriced_units >= 0),
    amount_millicents bigint NOT NULL CHECK (amount_millicents >= 0),
    lines jsonb NOT NULL CHECK (jsonb_typeof(lines) = 'array'),
    as_of timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    finalized_at timestamptz,
    FOREIGN KEY (account_id, platform_tenant_id)
      REFERENCES platform_tenants(account_id, id) ON DELETE CASCADE,
    CHECK (period_start = date_trunc('minute', period_start)
       AND period_end = date_trunc('minute', period_end) AND period_end > period_start),
    CHECK ((status IN ('draft', 'superseded') AND finalized_at IS NULL)
        OR (status = 'finalized' AND finalized_at IS NOT NULL)),
    UNIQUE (platform_tenant_id, period_start, period_end, revision)
);
CREATE INDEX IF NOT EXISTS platform_tenant_statements_period_idx ON platform_tenant_statements
  (account_id, platform_tenant_id, period_start DESC, revision DESC);

-- Normalized participation supports exact consumer/window overlap checks at
-- handoff. The priced minute-level snapshot remains immutable in lines.
CREATE TABLE IF NOT EXISTS platform_tenant_statement_consumers (
    statement_id uuid NOT NULL REFERENCES platform_tenant_statements(id) ON DELETE CASCADE,
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    consumer_id uuid NOT NULL REFERENCES api_consumers(id) ON DELETE CASCADE,
    PRIMARY KEY (statement_id, app_id, consumer_id)
);
CREATE INDEX IF NOT EXISTS platform_tenant_statement_consumers_lookup_idx
  ON platform_tenant_statement_consumers (app_id, consumer_id, statement_id);

CREATE TABLE IF NOT EXISTS platform_tenant_statement_handoffs (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    platform_tenant_id uuid NOT NULL REFERENCES platform_tenants(id) ON DELETE CASCADE,
    statement_id uuid NOT NULL UNIQUE REFERENCES platform_tenant_statements(id) ON DELETE CASCADE,
    external_invoice_id text NOT NULL CHECK (length(external_invoice_id) BETWEEN 1 AND 255),
    currency text NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
    amount_millicents bigint NOT NULL CHECK (amount_millicents >= 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (account_id, external_invoice_id)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS platform_tenant_statement_handoffs;
DROP TABLE IF EXISTS platform_tenant_statement_consumers;
DROP TABLE IF EXISTS platform_tenant_statements;
-- +goose StatementEnd
