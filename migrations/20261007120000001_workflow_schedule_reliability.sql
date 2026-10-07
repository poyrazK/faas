-- +goose Up
ALTER TABLE workflow_schedule_cursors ADD COLUMN last_admitted_at timestamptz;
ALTER TABLE platform_tenant_workflow_schedule_cursors ADD COLUMN last_admitted_at timestamptz;
-- Seed priority from the latest surviving admission; future skips preserve it.
UPDATE workflow_schedule_cursors c SET last_admitted_at = r.scheduled_for
FROM workflow_runs r WHERE r.id = c.last_run_id;
UPDATE platform_tenant_workflow_schedule_cursors c SET last_admitted_at = r.scheduled_for
FROM workflow_runs r WHERE r.id = c.last_run_id;

CREATE TABLE workflow_schedule_occurrences (
 id uuid PRIMARY KEY,
 app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 platform_tenant_id uuid REFERENCES platform_tenants(id) ON DELETE CASCADE,
 workflow_name text NOT NULL CHECK (workflow_name <> ''),
 deployment_id uuid NOT NULL,
 scheduled_for timestamptz NOT NULL,
 evaluated_at timestamptz NOT NULL,
 status text NOT NULL CHECK (status IN ('started', 'skipped_overlap', 'skipped_quota')),
 run_id uuid,
 CHECK ((status = 'started') = (run_id IS NOT NULL)),
 UNIQUE NULLS NOT DISTINCT (app_id, platform_tenant_id, workflow_name, scheduled_for)
);
CREATE INDEX workflow_schedule_occurrences_history_idx
 ON workflow_schedule_occurrences (app_id, scheduled_for DESC, id DESC);
CREATE INDEX workflow_schedule_occurrences_retention_idx
 ON workflow_schedule_occurrences (evaluated_at, id);
CREATE INDEX workflow_schedule_occurrences_quota_idx
 ON workflow_schedule_occurrences (app_id, evaluated_at) WHERE status = 'skipped_quota';

-- +goose Down
DROP TABLE workflow_schedule_occurrences;
ALTER TABLE platform_tenant_workflow_schedule_cursors DROP COLUMN last_admitted_at;
ALTER TABLE workflow_schedule_cursors DROP COLUMN last_admitted_at;
