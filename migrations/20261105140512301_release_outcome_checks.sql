-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';

-- ADR-1448 amendment 1: a release command that started but ended without an
-- exit code (its worker lease expired, it timed out or was cancelled while
-- running) left the database in an unknown state. The next release command of
-- the app scope waits until a person records that they checked the database.
CREATE TABLE IF NOT EXISTS release_outcome_checks (
    task_id UUID PRIMARY KEY REFERENCES app_tasks(id) ON DELETE CASCADE,
    account_id UUID NOT NULL,
    app_id UUID NOT NULL,
    deployment_id UUID NOT NULL,
    checked_by TEXT NOT NULL CHECK (length(checked_by) BETWEEN 1 AND 200),
    note TEXT NOT NULL CHECK (length(btrim(note)) BETWEEN 1 AND 1000),
    checked_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS release_outcome_checks_app_idx
    ON release_outcome_checks (account_id, app_id);

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TABLE release_outcome_checks;
