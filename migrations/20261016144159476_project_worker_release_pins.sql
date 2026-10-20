-- migration-compat: expand
-- Applied online (ADR-202610171900): every goose statement is its own short
-- transaction locking at most three existing tables. Every step is idempotent.
-- +goose NO TRANSACTION
-- +goose Up
SET lock_timeout = '5s';
CREATE TABLE IF NOT EXISTS project_worker_release_pins (
  root_invocation_id uuid PRIMARY KEY REFERENCES invocations(id) ON DELETE CASCADE,
  app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
  binding_id uuid NOT NULL REFERENCES queue_bindings(id) ON DELETE CASCADE,
  release_id uuid NOT NULL,
  deployment_id uuid NOT NULL,
  created_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK (isfinite(created_at))
);
-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='project_worker_release_pins'::regclass AND conname='project_worker_release_pins_release_id_fkey') THEN
  ALTER TABLE project_worker_release_pins ADD CONSTRAINT project_worker_release_pins_release_id_fkey FOREIGN KEY (release_id) REFERENCES project_release_sets(id);
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='project_worker_release_pins'::regclass AND conname='project_worker_release_pins_deployment_id_fkey') THEN
  ALTER TABLE project_worker_release_pins ADD CONSTRAINT project_worker_release_pins_deployment_id_fkey FOREIGN KEY (deployment_id) REFERENCES deployments(id);
 END IF;
END $$;
-- +goose StatementEnd
CREATE INDEX IF NOT EXISTS project_worker_release_pins_release ON project_worker_release_pins(release_id);

-- +goose Down
DROP TABLE project_worker_release_pins;
