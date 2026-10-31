-- filename: 20261031220612000_resource_labels.sql

-- ADR-20261031100025 step 5: managed databases, crons and jobs carry labels,
-- as apps do (20261031142449136_app_labels). Only apid writes the columns;
-- nothing on the request path reads them. The constant default makes each
-- column a metadata-only change and an older apid ignores it.
-- migration-compat: expand
--
-- Each object CHECK is added NOT VALID and validated in its own statement, so
-- the tables keep being written during the scan. Every step is re-runnable.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';

ALTER TABLE managed_postgres_databases ADD COLUMN IF NOT EXISTS labels jsonb NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE crons ADD COLUMN IF NOT EXISTS labels jsonb NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE jobs ADD COLUMN IF NOT EXISTS labels jsonb NOT NULL DEFAULT '{}'::jsonb;

-- +goose StatementBegin
DO $labels$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'managed_postgres_databases'::regclass AND conname = 'managed_postgres_databases_labels_object_chk') THEN
    ALTER TABLE managed_postgres_databases ADD CONSTRAINT managed_postgres_databases_labels_object_chk
      CHECK (jsonb_typeof(labels) = 'object') NOT VALID;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'crons'::regclass AND conname = 'crons_labels_object_chk') THEN
    ALTER TABLE crons ADD CONSTRAINT crons_labels_object_chk
      CHECK (jsonb_typeof(labels) = 'object') NOT VALID;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'jobs'::regclass AND conname = 'jobs_labels_object_chk') THEN
    ALTER TABLE jobs ADD CONSTRAINT jobs_labels_object_chk
      CHECK (jsonb_typeof(labels) = 'object') NOT VALID;
  END IF;
END
$labels$;
-- +goose StatementEnd

ALTER TABLE managed_postgres_databases VALIDATE CONSTRAINT managed_postgres_databases_labels_object_chk;
ALTER TABLE crons VALIDATE CONSTRAINT crons_labels_object_chk;
ALTER TABLE jobs VALIDATE CONSTRAINT jobs_labels_object_chk;

-- +goose Down
SET lock_timeout = '5s';

ALTER TABLE jobs DROP CONSTRAINT IF EXISTS jobs_labels_object_chk;
ALTER TABLE jobs DROP COLUMN IF EXISTS labels;
ALTER TABLE crons DROP CONSTRAINT IF EXISTS crons_labels_object_chk;
ALTER TABLE crons DROP COLUMN IF EXISTS labels;
ALTER TABLE managed_postgres_databases DROP CONSTRAINT IF EXISTS managed_postgres_databases_labels_object_chk;
ALTER TABLE managed_postgres_databases DROP COLUMN IF EXISTS labels;
