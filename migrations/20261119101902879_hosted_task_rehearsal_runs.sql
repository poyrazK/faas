-- migration-compat: expand
-- filename: 20261119101902879_hosted_task_rehearsal_runs.sql

-- +goose Up
SET LOCAL lock_timeout = '5s';

-- A claimed approval reserves one native run identity. The reservation is
-- separate from the plan so an older plan writer cannot overwrite it.
CREATE TABLE IF NOT EXISTS hosted_task_rehearsal_runs (
    account_id uuid NOT NULL,
    id text NOT NULL,
    run_id uuid NOT NULL UNIQUE,
    plan_sha256 text NOT NULL CHECK (plan_sha256 ~ '^[0-9a-f]{64}$'),
    reserved_at timestamptz NOT NULL,
    approval_expires_at timestamptz NOT NULL,
    PRIMARY KEY (account_id, id),
    FOREIGN KEY (account_id, id) REFERENCES hosted_task_rehearsal_plans(account_id, id) ON DELETE CASCADE,
    CHECK (reserved_at < approval_expires_at)
);

-- +goose Down
DROP TABLE IF EXISTS hosted_task_rehearsal_runs;
