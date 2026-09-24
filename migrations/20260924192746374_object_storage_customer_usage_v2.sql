-- filename: 20260924192746374_object_storage_customer_usage_v2.sql

-- +goose Up
-- Shadow-only customer quantities. The legacy usage report remains the sole
-- admission and month-close source until a separately qualified cutover.
CREATE TABLE IF NOT EXISTS object_storage_customer_usage_reports_v2 (
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    backend_id text NOT NULL CHECK (length(backend_id) BETWEEN 1 AND 63),
    backend_fingerprint text NOT NULL CHECK (backend_fingerprint ~ '^[0-9a-f]{64}$'),
    source text NOT NULL CHECK (length(source) BETWEEN 1 AND 128),
    version smallint NOT NULL CHECK (version = 2),
    period_start timestamptz NOT NULL CHECK (period_start = date_trunc('month', period_start AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'),
    coverage_end timestamptz NOT NULL CHECK (coverage_end >= period_start AND coverage_end <= period_start + interval '1 month'),
    observed_at timestamptz NOT NULL CHECK (observed_at >= coverage_end),
    evidence_digest text NOT NULL CHECK (evidence_digest ~ '^[0-9a-f]{64}$'),
    stored_byte_hours bigint NOT NULL CHECK (stored_byte_hours BETWEEN 0 AND 1152921504606846976),
    read_operations bigint NOT NULL CHECK (read_operations BETWEEN 0 AND 1152921504606846976),
    write_operations bigint NOT NULL CHECK (write_operations BETWEEN 0 AND 1152921504606846976),
    egress_bytes bigint CHECK (egress_bytes BETWEEN 0 AND 1152921504606846976),
    PRIMARY KEY (account_id, backend_id, period_start, observed_at)
);

-- +goose Down
DROP TABLE IF EXISTS object_storage_customer_usage_reports_v2;
