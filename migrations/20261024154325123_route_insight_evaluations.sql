-- migration-compat: expand
-- filename: 20261024154325123_route_insight_evaluations.sql

-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
-- ADR-20261024154324: when apid's route insights evaluator last took each
-- app. The evaluator walks the apps that ran least recently evaluated first
-- and claims each one here, so every app on a plan with request telemetry
-- is evaluated however many there are, and two apid replicas never evaluate
-- one app in the same run.
CREATE TABLE IF NOT EXISTS route_insight_evaluations (
    app_id uuid PRIMARY KEY REFERENCES apps(id) ON DELETE CASCADE,
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    evaluated_at timestamptz NOT NULL CHECK (isfinite(evaluated_at))
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS route_insight_evaluations;
-- +goose StatementEnd
