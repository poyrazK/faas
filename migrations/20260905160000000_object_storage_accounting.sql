-- +goose Up
-- All objects tolerate migration-ledger replay; never reset accounting data.
CREATE TABLE IF NOT EXISTS object_storage_bucket_usage (
    bucket_id uuid PRIMARY KEY REFERENCES object_buckets(id) ON DELETE CASCADE,
    baseline_bytes bigint NOT NULL DEFAULT 0 CHECK (baseline_bytes >= 0),
    baseline_keys bigint NOT NULL DEFAULT 0 CHECK (baseline_keys >= 0),
    granted_bytes bigint NOT NULL DEFAULT 0 CHECK (granted_bytes >= 0),
    granted_keys bigint NOT NULL DEFAULT 0 CHECK (granted_keys >= 0),
    observed_bytes bigint NOT NULL DEFAULT 0 CHECK (observed_bytes >= 0),
    observed_keys bigint NOT NULL DEFAULT 0 CHECK (observed_keys >= 0),
    observed_at timestamptz,
    attempt_at timestamptz,
    lease_until timestamptz,
    token text NOT NULL DEFAULT '',
    CHECK ((token = '' AND lease_until IS NULL) OR (token <> '' AND lease_until IS NOT NULL))
);
CREATE TABLE IF NOT EXISTS object_storage_key_grants (
    bucket_id uuid NOT NULL REFERENCES object_buckets(id) ON DELETE CASCADE,
    key_hash text NOT NULL CHECK (length(key_hash) = 64),
    max_bytes bigint NOT NULL CHECK (max_bytes BETWEEN 0 AND 5368709120),
    PRIMARY KEY (bucket_id, key_hash)
);
CREATE TABLE IF NOT EXISTS object_storage_authorizations (
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    period_start timestamptz NOT NULL CHECK (period_start = date_trunc('month', period_start AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'),
    count bigint NOT NULL CHECK (count > 0),
    PRIMARY KEY (account_id, period_start)
);
CREATE TABLE IF NOT EXISTS object_storage_inventory_samples (
    token text PRIMARY KEY CHECK (token <> ''),
    bucket_id uuid NOT NULL REFERENCES object_buckets(id) ON DELETE CASCADE,
    observed_at timestamptz NOT NULL,
    bytes bigint NOT NULL CHECK (bytes >= 0),
    objects bigint NOT NULL CHECK (objects >= 0)
);
CREATE TABLE IF NOT EXISTS object_storage_usage_reports (
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    backend_id text NOT NULL CHECK (length(backend_id) BETWEEN 1 AND 63),
    backend_fingerprint text NOT NULL CHECK (length(backend_fingerprint) = 64),
    source text NOT NULL CHECK (length(source) BETWEEN 1 AND 128),
    period_start timestamptz NOT NULL CHECK (period_start = date_trunc('month', period_start AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'),
    observed_at timestamptz NOT NULL CHECK (observed_at >= period_start AND observed_at < period_start + interval '1 month'),
    stored_byte_hours bigint NOT NULL CHECK (stored_byte_hours BETWEEN 0 AND 1152921504606846976),
    request_count bigint NOT NULL CHECK (request_count BETWEEN 0 AND 1152921504606846976),
    egress_bytes bigint NOT NULL CHECK (egress_bytes BETWEEN 0 AND 1152921504606846976),
    cost_millicents bigint NOT NULL CHECK (cost_millicents BETWEEN 0 AND 1152921504606846976),
    PRIMARY KEY (account_id, backend_id, period_start, observed_at)
);
CREATE TABLE IF NOT EXISTS object_storage_usage_heads (
    account_id uuid NOT NULL,
    backend_id text NOT NULL,
    period_start timestamptz NOT NULL,
    observed_at timestamptz NOT NULL,
    PRIMARY KEY (account_id, period_start, backend_id),
    FOREIGN KEY (account_id, backend_id, period_start, observed_at)
        REFERENCES object_storage_usage_reports(account_id, backend_id, period_start, observed_at) ON DELETE CASCADE
);

-- +goose Down
DROP TABLE object_storage_usage_heads;
DROP TABLE object_storage_usage_reports;
DROP TABLE object_storage_inventory_samples;
DROP TABLE object_storage_authorizations;
DROP TABLE object_storage_key_grants;
DROP TABLE object_storage_bucket_usage;
