-- Offline projection of ADR-20261018000200; never installed directly.
CREATE TABLE scenario_postgres_faults (
 id uuid PRIMARY KEY, resource_id uuid NOT NULL, account_id uuid NOT NULL,
 run_id text NOT NULL, app_id uuid NOT NULL, owner_id uuid NOT NULL,
 phase text NOT NULL, receipt jsonb NOT NULL
);
CREATE TABLE scenario_test_action_policies (
 app_id uuid,account_id uuid,run_id text,workload_name text,managed_work_plan jsonb,
 sealed_at timestamptz,recorder_closing boolean,managed_work_failed boolean
);
CREATE TABLE scenario_postgres_fixtures (
 id uuid,account_id uuid,run_id text,owner_id uuid,status text,backend_sha256 text,expires_at timestamptz
);
CREATE TABLE instances (id uuid,app_id uuid,deployment_id uuid,state text);
CREATE TABLE invocations (id uuid,app_id uuid,instance_id text,source text,state text,attempts integer,lease_expires_at timestamptz,replay_generation integer);
CREATE TABLE scenario_recorder_sessions (id uuid,app_id uuid,instance_id uuid,deployment_id uuid,status text);
CREATE TABLE scenario_recorder_requests (id uuid,session_id uuid,attempt_id uuid,status text);
CREATE TABLE deployments (id uuid,app_id uuid);
