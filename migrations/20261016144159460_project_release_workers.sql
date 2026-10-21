-- migration-compat: expand
-- Applied online (ADR-202610171900): every goose statement is its own short
-- transaction locking at most three existing tables. Every step is idempotent.
-- +goose NO TRANSACTION
-- +goose Up
SET lock_timeout = '5s';
CREATE TABLE IF NOT EXISTS deployment_project_release_workers (
 deployment_id uuid PRIMARY KEY REFERENCES deployments(id) ON DELETE CASCADE,
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 project_id uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
 attempt_id text NOT NULL CHECK(attempt_id ~ '^[0-9a-f]{32}$'),
 FOREIGN KEY(account_id,attempt_id) REFERENCES project_submissions(account_id,attempt_id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS deployment_project_release_workers_app_idx ON deployment_project_release_workers(project_id,attempt_id);
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_project_release_worker_identity() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='UPDATE' OR pg_trigger_depth()=1
 THEN RAISE EXCEPTION 'project release worker identity is immutable' USING ERRCODE='23514'; END IF;
 RETURN OLD;
END $$;
-- +goose StatementEnd
CREATE OR REPLACE TRIGGER immutable_project_release_worker BEFORE UPDATE OR DELETE ON deployment_project_release_workers
 FOR EACH ROW EXECUTE FUNCTION guard_project_release_worker_identity();
CREATE TABLE IF NOT EXISTS project_release_worker_drains (
 instance_id uuid PRIMARY KEY REFERENCES instances(id) ON DELETE CASCADE,
 requested_at timestamptz NOT NULL DEFAULT now() CHECK(isfinite(requested_at))
);
-- +goose Down
DROP TABLE project_release_worker_drains;
DROP TABLE deployment_project_release_workers;
DROP FUNCTION guard_project_release_worker_identity();
