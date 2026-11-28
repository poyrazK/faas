-- filename: 20261127180000200_alert_failure_count_indexes.sql
-- migration-compat: expand
-- The alert metrics added in 20261127180000100 run once a minute per rule:
-- - job_failures counts an account's job runs that finished failed or
--   dead-lettered within the window. job_runs_account_idx orders by
--   created_at, so the count would walk every run the account ever made;
--   job_runs_failed_finished_idx holds only failed runs.
-- - boot_failures counts one app's failed boots. events_kind_at_idx has no
--   app key, so a fleet-wide incident would make every rule read every
--   failed boot in its window; events_boot_failed_app_at_idx keys them by app.
-- Built concurrently so job and event writes never wait on the build.
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
 IF EXISTS (SELECT 1 FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid
            WHERE c.relname = 'events_boot_failed_app_at_idx' AND NOT i.indisvalid) THEN
  EXECUTE 'DROP INDEX events_boot_failed_app_at_idx';
 END IF;
END $$;
-- +goose StatementEnd

CREATE INDEX CONCURRENTLY IF NOT EXISTS job_runs_failed_finished_idx
    ON job_runs (account_id, finished_at DESC)
    WHERE aggregate_status IN ('failed', 'dead_letter');

CREATE INDEX CONCURRENTLY IF NOT EXISTS events_boot_failed_app_at_idx
    ON events ((data->>'app_id'), at DESC)
    WHERE kind = 'wake.boot_failed';

RESET statement_timeout;
RESET lock_timeout;

-- +goose Down
SET lock_timeout = '5s';
DROP INDEX CONCURRENTLY IF EXISTS events_boot_failed_app_at_idx;
DROP INDEX CONCURRENTLY IF EXISTS job_runs_failed_finished_idx;
RESET lock_timeout;
