-- +goose Up
ALTER TABLE managed_realtime_consumer_groups ADD COLUMN IF NOT EXISTS retry_policy jsonb NOT NULL DEFAULT '{"enabled":false,"base_delay_seconds":1,"max_delay_seconds":300,"jitter":true,"permanent_failure_codes":[]}'::jsonb CHECK (jsonb_typeof(retry_policy)='object');
-- +goose Down
ALTER TABLE managed_realtime_consumer_groups DROP COLUMN retry_policy;
-- Already scheduled retries retain their available_at timestamps.
