-- filename: 20260912094529228_api_consumer_usage_statements.sql

-- +goose Up
-- +goose StatementBegin
-- Durable snapshots of API consumer usage quotes. The JSON bucket payload is
-- deliberately stored with the statement so later rate-card changes cannot
-- rewrite an already-issued payable record.
CREATE TABLE IF NOT EXISTS api_consumer_usage_statements (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id          uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    app_id              uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    consumer_id         uuid NOT NULL REFERENCES api_consumers(id) ON DELETE CASCADE,
    period_start        timestamptz NOT NULL,
    period_end          timestamptz NOT NULL,
    status              text NOT NULL DEFAULT 'draft',
    currency            text,
    billable_units      bigint NOT NULL,
    unpriced_units      bigint NOT NULL,
    amount_millicents   bigint NOT NULL,
    priced              boolean NOT NULL,
    buckets             jsonb NOT NULL DEFAULT '[]'::jsonb,
    as_of               timestamptz NOT NULL,
    created_at          timestamptz NOT NULL DEFAULT now(),
    finalized_at        timestamptz,
    CONSTRAINT api_consumer_usage_statements_status_chk
      CHECK (status IN ('draft', 'finalized')),
    CONSTRAINT api_consumer_usage_statements_currency_chk
      CHECK (currency IS NULL OR currency ~ '^[A-Z]{3}$'),
    CONSTRAINT api_consumer_usage_statements_totals_chk
      CHECK (billable_units >= 0 AND unpriced_units >= 0 AND amount_millicents >= 0),
    CONSTRAINT api_consumer_usage_statements_period_chk
      CHECK (period_start = date_trunc('minute', period_start)
             AND period_end = date_trunc('minute', period_end)
             AND period_end > period_start),
    CONSTRAINT api_consumer_usage_statements_buckets_chk
      CHECK (jsonb_typeof(buckets) = 'array'),
    CONSTRAINT api_consumer_usage_statements_finalized_chk
      CHECK ((status = 'draft' AND finalized_at IS NULL)
             OR (status = 'finalized' AND finalized_at IS NOT NULL)),
    CONSTRAINT api_consumer_usage_statements_period_uniq
      UNIQUE (app_id, consumer_id, period_start, period_end)
);

CREATE INDEX IF NOT EXISTS api_consumer_usage_statements_account_app_consumer_idx
  ON api_consumer_usage_statements (account_id, app_id, consumer_id, period_start DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS api_consumer_usage_statements_account_app_consumer_idx;
DROP TABLE IF EXISTS api_consumer_usage_statements;
-- +goose StatementEnd
