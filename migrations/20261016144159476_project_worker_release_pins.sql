-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
CREATE TABLE IF NOT EXISTS project_worker_release_pins (
  root_invocation_id uuid PRIMARY KEY REFERENCES invocations(id) ON DELETE CASCADE,
  app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
  binding_id uuid NOT NULL REFERENCES queue_bindings(id) ON DELETE CASCADE,
  release_id uuid NOT NULL REFERENCES project_release_sets(id),
  deployment_id uuid NOT NULL REFERENCES deployments(id),
  created_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK (isfinite(created_at))
);
CREATE INDEX IF NOT EXISTS project_worker_release_pins_release ON project_worker_release_pins(release_id);

-- +goose Down
DROP TABLE project_worker_release_pins;
