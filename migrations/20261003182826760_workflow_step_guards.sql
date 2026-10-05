-- filename: 20261003182826760_workflow_step_guards.sql

-- +goose Up
ALTER TABLE workflow_steps
 ADD COLUMN IF NOT EXISTS when_matched boolean,
 ADD COLUMN IF NOT EXISTS when_evaluated_at timestamptz,
 ADD COLUMN IF NOT EXISTS skip_reason text;
ALTER TABLE workflow_steps
 DROP CONSTRAINT IF EXISTS workflow_steps_when_check,
 DROP CONSTRAINT IF EXISTS workflow_steps_skip_reason_check,
 ADD CONSTRAINT workflow_steps_when_check CHECK ((when_matched IS NULL) = (when_evaluated_at IS NULL)),
 ADD CONSTRAINT workflow_steps_skip_reason_check CHECK (skip_reason IS NULL OR skip_reason IN ('when_false', 'dependency_skipped', 'dependency_failed', 'route_not_taken'));

-- +goose Down
ALTER TABLE workflow_steps
 DROP COLUMN IF EXISTS when_matched,
 DROP COLUMN IF EXISTS when_evaluated_at,
 DROP COLUMN IF EXISTS skip_reason;
