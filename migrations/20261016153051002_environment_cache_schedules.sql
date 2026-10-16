-- filename: 20261016153051002_environment_cache_schedules.sql

-- +goose Up
CREATE TABLE IF NOT EXISTS environment_cache_schedules (
 schedule_id uuid PRIMARY KEY,
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 project_id uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
 environment_id uuid NOT NULL REFERENCES project_environments(id) ON DELETE CASCADE,
 scope text NOT NULL CHECK (length(scope) BETWEEN 1 AND 63),
 request jsonb NOT NULL CHECK (jsonb_typeof(request)='object'),
 execute_at timestamptz NOT NULL CHECK (isfinite(execute_at)),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 status text NOT NULL DEFAULT 'scheduled' CHECK (status IN ('scheduled','executing','executed','failed','cancelled')),
 lease_token uuid,
 lease_until timestamptz,
 finished_at timestamptz,
 error_code text NOT NULL DEFAULT '' CHECK (length(error_code)<=64),
 result jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(result)='object'),
 CHECK ((status='executing')=(lease_token IS NOT NULL AND lease_until IS NOT NULL)),
 CHECK ((status IN ('executed','failed','cancelled'))=(finished_at IS NOT NULL))
);
CREATE INDEX IF NOT EXISTS environment_cache_schedules_due ON environment_cache_schedules(execute_at) WHERE status IN ('scheduled','executing');
CREATE INDEX IF NOT EXISTS environment_cache_schedules_target ON environment_cache_schedules(account_id,app_id,environment_id);
-- +goose Down
DROP TABLE environment_cache_schedules;
