-- filename: 20260911172003420_debug_regression_observations_repair.sql

-- +goose Up
-- +goose StatementBegin

-- Migration 00436 is present in the release history, but a database can
-- legitimately have its ledger ahead of the table after a restored dump or
-- a partially-applied deploy.  Goose will not replay an already-recorded
-- migration, so repair the read surface in a new append-only migration.
-- Keep every operation idempotent: this migration runs on healthy databases
-- as well as on the drifted production shape.
CREATE TABLE IF NOT EXISTS debug_regression_observations (
    app_id            uuid         NOT NULL,
    deployment_id     uuid         NOT NULL,
    route             text         NOT NULL CHECK (length(route) BETWEEN 1 AND 256),
    p95_ms            int          NOT NULL CHECK (p95_ms >= 0),
    p95_base_ms       int          NOT NULL CHECK (p95_base_ms >= 0),
    affected_count    int          NOT NULL CHECK (affected_count >= 0),
    regression_factor numeric(5,2) NOT NULL CHECK (regression_factor >= 1.0),
    first_detected_at timestamptz  NOT NULL DEFAULT now(),
    last_detected_at  timestamptz  NOT NULL DEFAULT now(),
    PRIMARY KEY (app_id, deployment_id, route)
);

-- The ADD COLUMN guards repair a partially-created table as well as a table
-- that was dropped and recreated by an operator.  Existing rows receive the
-- timestamp defaults; nullable legacy columns remain queryable until the
-- operator can complete a deeper data repair.
ALTER TABLE debug_regression_observations
    ADD COLUMN IF NOT EXISTS app_id uuid,
    ADD COLUMN IF NOT EXISTS deployment_id uuid,
    ADD COLUMN IF NOT EXISTS route text,
    ADD COLUMN IF NOT EXISTS p95_ms int,
    ADD COLUMN IF NOT EXISTS p95_base_ms int,
    ADD COLUMN IF NOT EXISTS affected_count int,
    ADD COLUMN IF NOT EXISTS regression_factor numeric(5,2),
    ADD COLUMN IF NOT EXISTS first_detected_at timestamptz DEFAULT now(),
    ADD COLUMN IF NOT EXISTS last_detected_at timestamptz DEFAULT now();

CREATE INDEX IF NOT EXISTS debug_regression_observations_app_idx
    ON debug_regression_observations (app_id, last_detected_at DESC);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Forward-only repair: dropping the table would destroy observations written
-- after this migration and reintroduce the production debugger outage.
SELECT 1;
-- +goose StatementEnd
