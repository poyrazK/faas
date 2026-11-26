-- filename: 20261126214158202_api_sandbox_approval_checks.sql
-- migration-compat: expand

-- +goose Up
SET LOCAL lock_timeout = '5s';

CREATE TABLE IF NOT EXISTS api_sandbox_approval_checks (
    comparison_id uuid PRIMARY KEY REFERENCES api_sandbox_comparisons(id) ON DELETE CASCADE,
    baseline_artifact_pin text NOT NULL CHECK (char_length(baseline_artifact_pin) = 64),
    candidate_artifact_pin text NOT NULL CHECK (char_length(candidate_artifact_pin) = 64)
);

-- +goose Down
DROP TABLE api_sandbox_approval_checks;
