-- filename: 20261127180000200_job_runs_failed_finished_index.sql
-- migration-compat: expand
-- The job_failures alert metric (20261127180000100) counts an account's job
-- runs that finished failed or dead-lettered within the rule window, once a
-- minute per rule. job_runs_account_idx orders by created_at, so the count
-- would walk every run the account ever made. This partial index holds only
-- failed runs. Built concurrently so job writes never wait on the build.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';
SET statement_timeout = 0;

-- +goose StatementBegin
DO $$ BEGIN
 -- A failed concurrent build leaves an invalid index that IF NOT EXISTS
 -- would keep; drop it so a rerun builds it again.
 IF EXISTS (SELECT 1 FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid
            WHERE c.relname = 'job_runs_failed_finished_idx' AND NOT i.indisvalid) THEN
  EXECUTE 'DROP INDEX job_runs_failed_finished_idx';
 END IF;
END $$;
-- +goose StatementEnd

CREATE INDEX CONCURRENTLY IF NOT EXISTS job_runs_failed_finished_idx
    ON job_runs (account_id, finished_at DESC)
    WHERE aggregate_status IN ('failed', 'dead_letter');

-- +goose Down
SET lock_timeout = '5s';
DROP INDEX CONCURRENTLY IF EXISTS job_runs_failed_finished_idx;
