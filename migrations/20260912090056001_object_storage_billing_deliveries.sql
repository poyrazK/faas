-- +goose Up
-- +goose StatementBegin
-- Provider-qualified receipts make object-storage month-close delivery
-- idempotent across retries and preserve shadow/pre-activation decisions when
-- rollout mode changes later.
ALTER TABLE object_storage_billing_periods
    ADD CONSTRAINT object_storage_billing_periods_delivery_identity_key
    UNIQUE (id, account_id, period_start);
CREATE TABLE IF NOT EXISTS object_storage_billing_deliveries (
    provider text NOT NULL CHECK (provider <> ''),
    billing_record_id uuid NOT NULL,
    account_id uuid NOT NULL,
    period_start timestamptz NOT NULL CHECK (
        period_start = date_trunc('month', period_start AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'
    ),
    mode text NOT NULL CHECK (mode IN ('preactivation', 'plan_ineligible', 'shadow', 'live')),
    quantity_millicents bigint NOT NULL CHECK (quantity_millicents BETWEEN 0 AND 1152921504606846976),
    delivered_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (provider, billing_record_id),
    UNIQUE (provider, account_id, period_start),
    CHECK (mode NOT IN ('preactivation', 'plan_ineligible') OR quantity_millicents = 0),
    FOREIGN KEY (billing_record_id, account_id, period_start)
        REFERENCES object_storage_billing_periods(id, account_id, period_start)
        ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS object_storage_billing_deliveries_account_period_idx
    ON object_storage_billing_deliveries (account_id, period_start DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS object_storage_billing_deliveries;
ALTER TABLE object_storage_billing_periods
    DROP CONSTRAINT IF EXISTS object_storage_billing_periods_delivery_identity_key;
-- +goose StatementEnd
