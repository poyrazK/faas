-- Offline projection of migration 20261017210000001.
CREATE TABLE scenario_postgres_fixtures (
 id uuid PRIMARY KEY, account_id uuid NOT NULL, run_id text NOT NULL,
 app_id uuid NOT NULL, owner_id uuid NOT NULL, database_name text NOT NULL,
 role_name text NOT NULL, status text NOT NULL, spec jsonb NOT NULL,
 initial_snapshot jsonb NOT NULL DEFAULT '[]', evidence jsonb NOT NULL DEFAULT '{}', expires_at timestamptz NOT NULL,
 backend_sha256 text NOT NULL DEFAULT ''
);
CREATE TABLE scenario_test_action_policies (app_id uuid NOT NULL, account_id uuid NOT NULL, run_id text NOT NULL, network_mode text NOT NULL, simulation_plan jsonb, sealed_at timestamptz, recorder_closing boolean NOT NULL, simulation_failed boolean NOT NULL, managed_work_plan jsonb, workload_name text NOT NULL);
CREATE TABLE apps (id uuid, status text, preview_expires_at timestamptz);
CREATE TABLE deployments (id uuid,app_id uuid);
