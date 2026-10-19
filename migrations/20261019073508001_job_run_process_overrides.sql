-- filename: 20261019073508001_job_run_process_overrides.sql
-- migration-compat: expand

-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
-- ADR-1790: an image run's `gregale run -u/-w` overrides of the image's USER
-- and WORKDIR, snapshotted with the run like its command. NULL keeps the
-- image's own. A replay of the run keeps them.
ALTER TABLE job_runs
    ADD COLUMN IF NOT EXISTS process_user text,
    ADD COLUMN IF NOT EXISTS process_workdir text;
ALTER TABLE job_runs
    DROP CONSTRAINT IF EXISTS job_runs_process_user_chk,
    DROP CONSTRAINT IF EXISTS job_runs_process_workdir_chk;
ALTER TABLE job_runs
    ADD CONSTRAINT job_runs_process_user_chk
    CHECK (process_user IS NULL OR octet_length(process_user) BETWEEN 1 AND 256),
    ADD CONSTRAINT job_runs_process_workdir_chk
    CHECK (process_workdir IS NULL OR (octet_length(process_workdir) BETWEEN 1 AND 4096 AND left(process_workdir, 1) = '/'));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE job_runs DROP CONSTRAINT IF EXISTS job_runs_process_workdir_chk;
ALTER TABLE job_runs DROP CONSTRAINT IF EXISTS job_runs_process_user_chk;
ALTER TABLE job_runs DROP COLUMN IF EXISTS process_workdir;
ALTER TABLE job_runs DROP COLUMN IF EXISTS process_user;
-- +goose StatementEnd
