-- +goose Up
ALTER TABLE managed_realtime_consumer_groups ADD COLUMN IF NOT EXISTS claim_rate_per_second integer NOT NULL DEFAULT 0 CHECK (claim_rate_per_second BETWEEN 0 AND 1000);
ALTER TABLE managed_realtime_consumer_groups ADD COLUMN IF NOT EXISTS claim_rate_burst integer NOT NULL DEFAULT 1 CHECK (claim_rate_burst BETWEEN 1 AND 32);
ALTER TABLE managed_realtime_consumer_groups ADD COLUMN IF NOT EXISTS claim_rate_tat timestamptz NOT NULL DEFAULT clock_timestamp() CHECK (isfinite(claim_rate_tat));
ALTER TABLE managed_realtime_consumer_groups ADD COLUMN IF NOT EXISTS throttled_claim_requests bigint NOT NULL DEFAULT 0 CHECK (throttled_claim_requests>=0);
-- +goose Down
ALTER TABLE managed_realtime_consumer_groups DROP COLUMN claim_rate_per_second,DROP COLUMN claim_rate_burst,DROP COLUMN claim_rate_tat,DROP COLUMN throttled_claim_requests;
-- Active leases and settlements remain unaffected; rollback removes rate enforcement.
