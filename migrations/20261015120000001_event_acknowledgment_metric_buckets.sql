-- +goose Up

CREATE TABLE IF NOT EXISTS event_acknowledgment_metric_buckets (
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 subscription_id uuid NOT NULL,
 bucket_start timestamptz NOT NULL CHECK (isfinite(bucket_start) AND bucket_start=date_trunc('minute',bucket_start,'UTC')),
 kind text NOT NULL CHECK (kind IN ('renewed','renewal_rejected','expired')),
 occurrences bigint NOT NULL CHECK (occurrences BETWEEN 1 AND 2147483647),
 saturated boolean NOT NULL DEFAULT false,
 retain_until timestamptz NOT NULL CHECK (isfinite(retain_until) AND retain_until>bucket_start),
 PRIMARY KEY(account_id,app_id,subscription_id,bucket_start,kind)
);
CREATE INDEX IF NOT EXISTS event_acknowledgment_metric_buckets_retention ON event_acknowledgment_metric_buckets(retain_until);
CREATE INDEX IF NOT EXISTS event_acknowledgment_metric_buckets_app ON event_acknowledgment_metric_buckets(app_id);

-- +goose Down
SELECT 1;
