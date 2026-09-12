-- filename: 20260912130000001_api_consumer_usage_statement_handoffs.sql

-- +goose Up
-- +goose StatementBegin
-- A customer-owned billing system claims a finalized statement exactly once.
-- Gregale stores the immutable external invoice reference and the statement's
-- priced amount as a handoff receipt; it does not move money or become the
-- merchant of record.
CREATE TABLE IF NOT EXISTS api_consumer_usage_statement_handoffs (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id          uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    app_id              uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    consumer_id         uuid NOT NULL REFERENCES api_consumers(id) ON DELETE CASCADE,
    statement_id        uuid NOT NULL REFERENCES api_consumer_usage_statements(id) ON DELETE CASCADE,
    external_invoice_id text NOT NULL,
    currency            text,
    amount_millicents   bigint NOT NULL,
    created_at          timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT api_consumer_usage_statement_handoffs_external_id_chk
      CHECK (length(external_invoice_id) BETWEEN 1 AND 255),
    CONSTRAINT api_consumer_usage_statement_handoffs_currency_chk
      CHECK (currency IS NULL OR currency ~ '^[A-Z]{3}$'),
    CONSTRAINT api_consumer_usage_statement_handoffs_amount_chk
      CHECK (amount_millicents >= 0),
    CONSTRAINT api_consumer_usage_statement_handoffs_statement_uniq
      UNIQUE (statement_id),
    CONSTRAINT api_consumer_usage_statement_handoffs_external_uniq
      UNIQUE (account_id, external_invoice_id)
);

CREATE INDEX IF NOT EXISTS api_consumer_usage_statement_handoffs_account_app_consumer_idx
  ON api_consumer_usage_statement_handoffs (account_id, app_id, consumer_id, created_at DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS api_consumer_usage_statement_handoffs_account_app_consumer_idx;
DROP TABLE IF EXISTS api_consumer_usage_statement_handoffs;
-- +goose StatementEnd
