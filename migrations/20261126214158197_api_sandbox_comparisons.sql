-- filename: 20261126214158197_api_sandbox_comparisons.sql
-- migration-compat: expand

-- +goose Up
SET LOCAL lock_timeout = '5s';

CREATE TABLE IF NOT EXISTS api_sandbox_comparisons (
    id uuid PRIMARY KEY,
    baseline_run_id uuid NOT NULL UNIQUE REFERENCES api_sandbox_runs(id) ON DELETE CASCADE,
    candidate_run_id uuid NOT NULL UNIQUE REFERENCES api_sandbox_runs(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK (baseline_run_id <> candidate_run_id)
);

-- +goose Down
DROP TABLE api_sandbox_comparisons;
