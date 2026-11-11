-- migration-compat: expand
-- filename: 20261111145205161_login_failure_budget.sql

-- +goose Up
-- Failed password checks per email address, shared by every apid
-- (ADR-20261111150000). Each apid kept its own in-memory counts, so the
-- per-address budget multiplied by the number of apid processes and reset on
-- every restart. One row per keyed email hash holds a fixed window's count
-- and when the owner was last told; rows of unbound addresses are kept too,
-- so the 429 does not reveal which addresses have accounts. Expired rows are
-- swept by the apid that records the next failure. Every step is re-runnable.
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
CREATE TABLE IF NOT EXISTS login_failure_budgets (
    email_hash text NOT NULL,
    window_started_at timestamptz NOT NULL,
    failures integer NOT NULL,
    notified_at timestamptz,
    CONSTRAINT login_failure_budgets_pkey PRIMARY KEY (email_hash),
    CONSTRAINT login_failure_budgets_hash_check CHECK (char_length(email_hash) BETWEEN 16 AND 128),
    CONSTRAINT login_failure_budgets_failures_check CHECK (failures BETWEEN 1 AND 1000000),
    CONSTRAINT login_failure_budgets_times_check CHECK (
        isfinite(window_started_at) AND (notified_at IS NULL OR isfinite(notified_at)))
);
CREATE INDEX IF NOT EXISTS login_failure_budgets_window_idx ON login_failure_budgets (window_started_at);
-- +goose StatementEnd
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='faas_runtime') THEN
  GRANT SELECT, INSERT, UPDATE, DELETE ON login_failure_budgets TO faas_runtime;
 END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS login_failure_budgets;
-- +goose StatementEnd
