-- +goose Up
CREATE TABLE IF NOT EXISTS project_binding_verification_runs (
 id uuid PRIMARY KEY,
 account_id uuid NOT NULL REFERENCES accounts(id),
 project_id uuid NOT NULL REFERENCES projects(id),
 environment_slug text NOT NULL CHECK(environment_slug<>''),
 status text NOT NULL CHECK(status IN ('pending','passed','cancelled')),
 snapshot jsonb NOT NULL CHECK(jsonb_typeof(snapshot)='object'),
 created_at timestamptz NOT NULL,
 expires_at timestamptz NOT NULL CHECK(expires_at>created_at AND expires_at<=created_at+interval '600 seconds')
);
CREATE INDEX IF NOT EXISTS project_binding_verification_runs_project_time_idx ON project_binding_verification_runs(project_id,created_at DESC);
ALTER TABLE binding_verification_runs ADD COLUMN IF NOT EXISTS graph_run_id uuid REFERENCES project_binding_verification_runs(id);
CREATE INDEX IF NOT EXISTS binding_verification_runs_graph_idx ON binding_verification_runs(graph_run_id) WHERE graph_run_id IS NOT NULL;
-- +goose Down
ALTER TABLE binding_verification_runs DROP COLUMN graph_run_id;
DROP TABLE project_binding_verification_runs;
