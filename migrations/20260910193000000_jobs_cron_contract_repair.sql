-- filename: 20260910193000000_jobs_cron_contract_repair.sql
-- +goose Up
-- +goose StatementBegin

-- The public jobs API uses batch/recurring, while the original jobs schema
-- retained the earlier app/function vocabulary. Convert any rows written by
-- pre-API tooling to the closest supported kind before tightening the check.
UPDATE jobs SET kind = 'batch' WHERE kind IN ('app', 'function');

ALTER TABLE jobs DROP CONSTRAINT IF EXISTS jobs_kind_check;
ALTER TABLE jobs
    ADD CONSTRAINT jobs_kind_check CHECK (kind IN ('batch', 'recurring'));

-- Cron runs are an audit ledger. Deleting the schedule must retain completed
-- invocation history, so detach the historical row instead of rejecting the
-- cron delete through the default RESTRICT foreign key.
ALTER TABLE invocations DROP CONSTRAINT IF EXISTS invocations_cron_id_fkey;
ALTER TABLE invocations
    ADD CONSTRAINT invocations_cron_id_fkey
    FOREIGN KEY (cron_id) REFERENCES crons(id) ON DELETE SET NULL;

-- +goose StatementEnd

-- +goose Down
-- Forward-only data repair: restoring the old constraints would make the
-- public API unusable and could strand rows written after this migration.
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
