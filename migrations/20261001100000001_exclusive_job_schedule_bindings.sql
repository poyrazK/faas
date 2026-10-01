-- filename: 20261001100000001_exclusive_job_schedule_bindings.sql

-- +goose Up
-- +goose StatementBegin
ALTER TABLE exclusive_work_trigger_bindings
  DROP CONSTRAINT exclusive_work_trigger_bindings_source_check,
  ADD CONSTRAINT exclusive_work_trigger_bindings_source_check
    CHECK (source IN ('cron', 'inbound_webhook', 'broker', 'job_schedule')),
  ALTER COLUMN app_id DROP NOT NULL,
  ADD COLUMN job_id uuid REFERENCES jobs(id) ON DELETE CASCADE,
  ADD CONSTRAINT exclusive_work_trigger_bindings_target_check
    CHECK ((source = 'job_schedule' AND app_id IS NULL AND job_id IS NOT NULL
              AND trigger_id = job_id AND platform_tenant_id IS NULL)
        OR (source <> 'job_schedule' AND app_id IS NOT NULL AND job_id IS NULL));

CREATE INDEX exclusive_work_trigger_bindings_job_idx
  ON exclusive_work_trigger_bindings (account_id, job_id)
  WHERE source = 'job_schedule';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX exclusive_work_trigger_bindings_job_idx;
ALTER TABLE exclusive_work_trigger_bindings
  DROP CONSTRAINT exclusive_work_trigger_bindings_target_check,
  DROP COLUMN job_id,
  ALTER COLUMN app_id SET NOT NULL,
  DROP CONSTRAINT exclusive_work_trigger_bindings_source_check,
  ADD CONSTRAINT exclusive_work_trigger_bindings_source_check
    CHECK (source IN ('cron', 'inbound_webhook', 'broker'));
-- +goose StatementEnd
