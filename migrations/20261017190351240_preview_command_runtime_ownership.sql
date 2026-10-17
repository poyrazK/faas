-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
-- ADR-230 / ADR-566: a preview command's provisional VM ownership survives
-- scheduler restart. A missing acknowledgement is never a teardown receipt.
CREATE TABLE IF NOT EXISTS financial_preview_command_runtimes (
  task_id uuid NOT NULL REFERENCES app_tasks(id) ON DELETE CASCADE,
  lease_token uuid NOT NULL,
  account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
  deployment_id uuid NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
  node_id uuid NOT NULL REFERENCES compute_nodes(id),
  environment_id uuid,
  phase text NOT NULL CHECK (phase IN ('preparing', 'active', 'retired')),
  ram_mb integer NOT NULL CHECK (ram_mb > 0),
  cpu_millicores integer NOT NULL CHECK (cpu_millicores > 0),
  recorded_at timestamptz NOT NULL,
  retired_at timestamptz CHECK ((phase = 'retired') = (retired_at IS NOT NULL)),
  PRIMARY KEY (task_id, lease_token)
);
CREATE UNIQUE INDEX IF NOT EXISTS financial_preview_command_active_task_idx ON financial_preview_command_runtimes(task_id) WHERE phase != 'retired';
CREATE INDEX IF NOT EXISTS financial_preview_command_app_idx ON financial_preview_command_runtimes(account_id, app_id) WHERE phase != 'retired';

CREATE OR REPLACE FUNCTION financial_preview_command_held(target_app uuid, target_deployment uuid, observed_at timestamptz)
RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT EXISTS (
  SELECT 1 FROM apps a
  JOIN financial_budget_stops s ON s.account_id = a.account_id
  JOIN financial_budget_policies p ON p.account_id=s.account_id AND p.id=s.policy_id AND p.revision=s.revision
  LEFT JOIN deployments d ON d.id=target_deployment AND d.app_id=a.id
  LEFT JOIN deployment_runtime_environment_owners owner ON owner.deployment_id=d.id
  LEFT JOIN project_environments environment ON environment.id=nullif(p.spec->'scope'->>'id','')::uuid
  WHERE a.id=target_app AND a.preview_of_slug IS NOT NULL AND a.preview_of_slug!=''
   AND p.deleted_at IS NULL AND p.spec->>'enabled'='true'
   AND s.requested_at<=observed_at AND (s.resume_rule='manual' OR s.period_end>observed_at)
   AND CASE p.spec->'scope'->>'kind'
    WHEN 'account' THEN true
    WHEN 'app' THEN (p.spec->'scope'->>'id')::uuid=a.id
    WHEN 'project' THEN (p.spec->'scope'->>'id')::uuid=a.project_id
    WHEN 'environment' THEN environment.account_id=a.account_id AND environment.project_id=a.project_id
      AND (owner.environment_id=environment.id OR owner.deployment_id IS NULL AND
        (environment.slug=CASE WHEN coalesce(nullif(d.scope,''),'default')='default' THEN 'production' ELSE d.scope END))
    ELSE false END
 )
$$;
-- +goose StatementEnd

-- +goose Down
DROP FUNCTION financial_preview_command_held(uuid, uuid, timestamptz);
DROP TABLE financial_preview_command_runtimes;
