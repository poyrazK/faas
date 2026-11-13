-- filename: 20261112201856243_pipeline_retention_observations.sql
-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE data_pipelines ADD COLUMN IF NOT EXISTS wal_retention jsonb NOT NULL DEFAULT '{}';
-- +goose StatementBegin
DO $migration$
BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='data_pipelines'::regclass AND conname='data_pipelines_wal_retention_check') THEN
  ALTER TABLE data_pipelines ADD CONSTRAINT data_pipelines_wal_retention_check
   CHECK (jsonb_typeof(wal_retention)='object' AND octet_length(wal_retention::text)<=4096) NOT VALID;
 END IF;
END $migration$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
ALTER TABLE data_pipelines DROP COLUMN IF EXISTS wal_retention;
