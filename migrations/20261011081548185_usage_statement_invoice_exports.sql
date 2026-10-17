-- filename: 20261011081548185_usage_statement_invoice_exports.sql

-- ADR-1300: export finalized usage statements to the account's own Stripe as
-- draft invoices. One sealed Stripe key per account; one export row per
-- statement, created before any Stripe call so a retry resumes the same
-- invoice instead of creating a second one.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS account_billing_connections (
    account_id    uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    provider      text NOT NULL,
    sealed_secret bytea NOT NULL,
    key_last4     text NOT NULL,
    livemode      boolean NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT account_billing_connections_pkey PRIMARY KEY (account_id, provider),
    CONSTRAINT account_billing_connections_provider_chk CHECK (provider IN ('stripe')),
    CONSTRAINT account_billing_connections_last4_chk CHECK (char_length(key_last4) = 4)
);

CREATE TABLE IF NOT EXISTS usage_statement_invoice_exports (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id          uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    source_kind         text NOT NULL,
    statement_id        uuid NOT NULL,
    provider            text NOT NULL,
    customer            text NOT NULL,
    status              text NOT NULL DEFAULT 'creating',
    provider_invoice_id text,
    currency            text NOT NULL,
    amount_minor        bigint NOT NULL,
    livemode            boolean NOT NULL,
    created_at          timestamptz NOT NULL DEFAULT now(),
    completed_at        timestamptz,
    CONSTRAINT usage_statement_invoice_exports_source_chk CHECK (source_kind IN ('app_consumer', 'platform_tenant')),
    CONSTRAINT usage_statement_invoice_exports_provider_chk CHECK (provider IN ('stripe')),
    CONSTRAINT usage_statement_invoice_exports_status_chk CHECK (status IN ('creating', 'completed')),
    CONSTRAINT usage_statement_invoice_exports_completed_chk
      CHECK ((status = 'completed') = (completed_at IS NOT NULL AND provider_invoice_id IS NOT NULL)),
    CONSTRAINT usage_statement_invoice_exports_currency_chk CHECK (currency ~ '^[A-Z]{3}$'),
    CONSTRAINT usage_statement_invoice_exports_statement_uniq UNIQUE (source_kind, statement_id)
);
CREATE INDEX IF NOT EXISTS usage_statement_invoice_exports_account_idx
  ON usage_statement_invoice_exports (account_id, created_at DESC);

COMMENT ON TABLE account_billing_connections IS
  'Sealed credentials for exporting usage statements to the account''s own payment provider; only key_last4 and livemode are ever returned.';
COMMENT ON TABLE usage_statement_invoice_exports IS
  'One draft provider invoice per finalized usage statement, recorded before provider calls so retries resume the same invoice.';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS usage_statement_invoice_exports;
DROP TABLE IF EXISTS account_billing_connections;
-- +goose StatementEnd
