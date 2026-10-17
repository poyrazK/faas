-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE data_pipeline_setups ADD COLUMN IF NOT EXISTS data_api_plan jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(data_api_plan)='object' AND octet_length(data_api_plan::text)<=16384);
ALTER TABLE data_pipeline_setups DROP CONSTRAINT data_pipeline_setups_phase_check;
ALTER TABLE data_pipeline_setups ADD CONSTRAINT data_pipeline_setups_phase_check CHECK (phase IN ('checking_source','provisioning','checking_mirror','starting','syncing','configuring_api','deploying_api','waiting_api','api_failed'));

-- +goose Down
SET LOCAL lock_timeout = '5s';
-- Stop unfinished API work before rolling back its orchestration schema.
UPDATE data_pipeline_setups SET state='cancelled',phase='syncing',lease_token='',lease_until=NULL WHERE phase IN ('configuring_api','deploying_api','waiting_api','api_failed');
ALTER TABLE data_pipeline_setups DROP CONSTRAINT data_pipeline_setups_phase_check;
ALTER TABLE data_pipeline_setups ADD CONSTRAINT data_pipeline_setups_phase_check CHECK (phase IN ('checking_source','provisioning','checking_mirror','starting','syncing'));
ALTER TABLE data_pipeline_setups DROP COLUMN data_api_plan;
