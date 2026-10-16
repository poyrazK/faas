-- +goose Up
CREATE TABLE IF NOT EXISTS binding_verification_runs (
 id uuid PRIMARY KEY,
 account_id uuid NOT NULL REFERENCES accounts(id),
 app_id uuid NOT NULL REFERENCES apps(id),
 deployment_id uuid NOT NULL REFERENCES deployments(id),
 scope text NOT NULL CHECK (scope <> ''),
 status text NOT NULL CHECK (status IN ('pending','passed','cancelled')),
 snapshot jsonb NOT NULL CHECK (jsonb_typeof(snapshot)='object'),
 created_at timestamptz NOT NULL,
 expires_at timestamptz NOT NULL CHECK (expires_at>created_at AND expires_at<=created_at+interval '600 seconds')
);
CREATE INDEX IF NOT EXISTS binding_verification_runs_app_time_idx ON binding_verification_runs(app_id,created_at DESC);
CREATE TABLE IF NOT EXISTS binding_verification_run_steps (
 run_id uuid NOT NULL REFERENCES binding_verification_runs(id) ON DELETE CASCADE,
 step_index integer NOT NULL CHECK (step_index>=0 AND step_index<128),
 resource_id uuid NOT NULL,
 resource_kind text NOT NULL CHECK (resource_kind IN ('task','queue')),
 PRIMARY KEY(run_id,step_index),
 UNIQUE(resource_kind,resource_id)
);
-- +goose Down
DROP TABLE binding_verification_run_steps;
DROP TABLE binding_verification_runs;
