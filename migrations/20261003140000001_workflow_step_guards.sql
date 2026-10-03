-- +goose Up
ALTER TABLE workflow_steps
 ADD COLUMN when_matched boolean,
 ADD COLUMN when_evaluated_at timestamptz,
 ADD COLUMN skip_reason text,
 ADD CONSTRAINT workflow_steps_when_check CHECK ((when_matched IS NULL) = (when_evaluated_at IS NULL)),
 ADD CONSTRAINT workflow_steps_skip_reason_check CHECK (skip_reason IS NULL OR skip_reason IN ('when_false', 'dependency_skipped', 'dependency_failed', 'route_not_taken'));

-- +goose Down
ALTER TABLE workflow_steps
 DROP COLUMN when_matched,
 DROP COLUMN when_evaluated_at,
 DROP COLUMN skip_reason;
