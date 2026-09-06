-- +goose Up
-- +goose StatementBegin
-- Immutable month-close snapshot for provider-neutral object-storage billing.
-- The rate card is copied into each row so later pricing changes cannot
-- rewrite an already-finalized customer period.
CREATE TABLE IF NOT EXISTS object_storage_billing_periods (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    period_start timestamptz NOT NULL CHECK (
        period_start = date_trunc('month', period_start AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'
    ),
    period_end timestamptz NOT NULL CHECK (period_end = period_start + interval '1 month'),
    currency text NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
    stored_byte_hours bigint NOT NULL CHECK (stored_byte_hours BETWEEN 0 AND 1152921504606846976),
    request_count bigint NOT NULL CHECK (request_count BETWEEN 0 AND 1152921504606846976),
    egress_bytes bigint NOT NULL CHECK (egress_bytes BETWEEN 0 AND 1152921504606846976),
    provider_cost_millicents bigint NOT NULL CHECK (provider_cost_millicents BETWEEN 0 AND 1152921504606846976),
    storage_millicents_per_gib_month bigint NOT NULL CHECK (storage_millicents_per_gib_month BETWEEN 0 AND 1152921504606846976),
    requests_millicents_per_million bigint NOT NULL CHECK (requests_millicents_per_million BETWEEN 0 AND 1152921504606846976),
    egress_millicents_per_gib bigint NOT NULL CHECK (egress_millicents_per_gib BETWEEN 0 AND 1152921504606846976),
    storage_millicents bigint NOT NULL CHECK (storage_millicents BETWEEN 0 AND 1152921504606846976),
    requests_millicents bigint NOT NULL CHECK (requests_millicents BETWEEN 0 AND 1152921504606846976),
    egress_millicents bigint NOT NULL CHECK (egress_millicents BETWEEN 0 AND 1152921504606846976),
    total_millicents bigint NOT NULL CHECK (total_millicents BETWEEN 0 AND 1152921504606846976),
    finalized_at timestamptz NOT NULL DEFAULT now(),
    CHECK (total_millicents = storage_millicents + requests_millicents + egress_millicents),
    UNIQUE (account_id, period_start)
);
CREATE INDEX IF NOT EXISTS object_storage_billing_periods_account_period_idx
    ON object_storage_billing_periods (account_id, period_start DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS object_storage_billing_periods;
-- +goose StatementEnd
