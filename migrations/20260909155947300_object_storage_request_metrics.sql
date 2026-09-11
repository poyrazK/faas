-- +goose Up
-- The branded gateway records every outbound provider request attempt. OVH's
-- public usage API does not expose an object-request counter, so this durable
-- ledger is retained for low-latency diagnostics and gateway-only sources;
-- the OVH GA exporter uses the provider's server access-log stream so direct
-- S3 URLs are covered as well.
CREATE TABLE IF NOT EXISTS object_storage_request_metrics (
    bucket_id uuid NOT NULL REFERENCES object_buckets(id) ON DELETE CASCADE,
    period_start timestamptz NOT NULL CHECK (period_start = date_trunc('month', period_start AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'),
    request_count bigint NOT NULL DEFAULT 0 CHECK (request_count BETWEEN 0 AND 1152921504606846976),
    PRIMARY KEY (bucket_id, period_start)
);

CREATE INDEX IF NOT EXISTS object_storage_request_metrics_period_idx
    ON object_storage_request_metrics (period_start, bucket_id);

-- +goose Down
DROP TABLE object_storage_request_metrics;
