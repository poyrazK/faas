-- filename: 20261009133655062_route_monitor_on_violation.sql

-- +goose Up
-- +goose StatementBegin
-- ADR-845: opt-in automatic rollback on a confirmed production error-budget
-- incident. Existing monitors keep report-only behavior.
ALTER TABLE route_monitors
 ADD COLUMN IF NOT EXISTS on_violation text NOT NULL DEFAULT 'report';
ALTER TABLE route_monitors
 DROP CONSTRAINT IF EXISTS route_monitors_on_violation_check;
ALTER TABLE route_monitors
 ADD CONSTRAINT route_monitors_on_violation_check
 CHECK (on_violation IN ('report', 'rollback'));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE route_monitors DROP CONSTRAINT IF EXISTS route_monitors_on_violation_check;
ALTER TABLE route_monitors DROP COLUMN IF EXISTS on_violation;
-- +goose StatementEnd
