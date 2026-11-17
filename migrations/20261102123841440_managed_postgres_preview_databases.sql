-- filename: 20261102123841440_managed_postgres_preview_databases.sql

-- ADR-20261102123523: a production database's previews setting gives each PR
-- preview environment a database of its own. The source database records the
-- setting ('off' by default, every existing row); a preview database records
-- its source, its environment (the project, or a legacy preview's production
-- app) and its pull request. The unique index makes setup idempotent across
-- retries and apid replicas. Only apid writes the columns; an older apid
-- neither sets up nor removes preview databases.
-- migration-compat: expand
--
-- The CHECKs are added NOT VALID and validated in their own statements, and
-- the index is built concurrently, so databases keep being written. Every
-- step is re-runnable: a failed concurrent build leaves an invalid index,
-- which the next run drops and builds again.
-- +goose NO TRANSACTION

-- +goose Up
SET statement_timeout = 0;
SET lock_timeout = '5s';

ALTER TABLE managed_postgres_databases
  ADD COLUMN IF NOT EXISTS previews text NOT NULL DEFAULT 'off',
  ADD COLUMN IF NOT EXISTS preview_source_database_id uuid,
  ADD COLUMN IF NOT EXISTS preview_environment_id text,
  ADD COLUMN IF NOT EXISTS preview_pr_number integer;

-- +goose StatementBegin
DO $previews$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'managed_postgres_databases'::regclass AND conname = 'managed_postgres_databases_previews_chk') THEN
    ALTER TABLE managed_postgres_databases ADD CONSTRAINT managed_postgres_databases_previews_chk
      CHECK (previews IN ('off', 'empty', 'copy')) NOT VALID;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'managed_postgres_databases'::regclass AND conname = 'managed_postgres_preview_fields_chk') THEN
    ALTER TABLE managed_postgres_databases ADD CONSTRAINT managed_postgres_preview_fields_chk
      CHECK ((preview_source_database_id IS NULL AND preview_environment_id IS NULL AND preview_pr_number IS NULL)
        OR (preview_source_database_id IS NOT NULL AND preview_source_database_id <> id
          AND preview_environment_id ~ '^[A-Za-z0-9-]{1,64}$'
          AND preview_pr_number > 0 AND previews = 'off')) NOT VALID;
  END IF;
END
$previews$;
-- +goose StatementEnd

ALTER TABLE managed_postgres_databases VALIDATE CONSTRAINT managed_postgres_databases_previews_chk;
ALTER TABLE managed_postgres_databases VALIDATE CONSTRAINT managed_postgres_preview_fields_chk;

-- +goose StatementBegin
DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid
             WHERE c.relname = 'managed_postgres_preview_key' AND NOT i.indisvalid) THEN
    EXECUTE 'DROP INDEX managed_postgres_preview_key';
  END IF;
END $$;
-- +goose StatementEnd

RESET lock_timeout;

CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS managed_postgres_preview_key
  ON managed_postgres_databases (preview_source_database_id, preview_environment_id, preview_pr_number)
  WHERE preview_source_database_id IS NOT NULL AND deleted_at IS NULL;

RESET statement_timeout;

-- +goose Down
SET statement_timeout = 0;
SET lock_timeout = '5s';

DROP INDEX CONCURRENTLY IF EXISTS managed_postgres_preview_key;
ALTER TABLE managed_postgres_databases DROP CONSTRAINT IF EXISTS managed_postgres_preview_fields_chk;
ALTER TABLE managed_postgres_databases DROP CONSTRAINT IF EXISTS managed_postgres_databases_previews_chk;
ALTER TABLE managed_postgres_databases
  DROP COLUMN IF EXISTS preview_pr_number,
  DROP COLUMN IF EXISTS preview_environment_id,
  DROP COLUMN IF EXISTS preview_source_database_id,
  DROP COLUMN IF EXISTS previews;

RESET lock_timeout;
RESET statement_timeout;
