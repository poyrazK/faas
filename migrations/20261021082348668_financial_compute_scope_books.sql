-- filename: 20261021082348668_financial_compute_scope_books.sql
-- migration-compat: expand

-- +goose Up
-- The foreign key and the owner column lock their tables briefly.
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
-- ADR-1750, amendments 1 and 2: a strict project or environment cap is a
-- shared gross book that every attempt in the scope funds alongside its own
-- book. Leases record the books they fund in their payload (project_id,
-- environment_id) and the deployment they run (deployment_id).
CREATE TABLE IF NOT EXISTS financial_compute_scope_books (
  account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  scope_kind text NOT NULL CHECK (scope_kind IN ('project', 'environment')),
  scope_id uuid NOT NULL,
  period_start timestamptz NOT NULL,
  period_end timestamptz NOT NULL CHECK (period_end > period_start),
  payload jsonb NOT NULL CHECK (jsonb_typeof(payload) = 'object'),
  CHECK (COALESCE((payload->>'committed')::numeric >= 0 AND (payload->>'reserved')::numeric >= 0
    AND (payload->>'committed')::numeric + (payload->>'reserved')::numeric <= 9007199254740991, false)),
  PRIMARY KEY (account_id, scope_kind, scope_id, period_start)
);

-- An owner that registers this incarnation enforces project and environment
-- caps. A legacy vmmd leaves it stale, so routing refuses strict work there.
ALTER TABLE financial_compute_owners ADD COLUMN IF NOT EXISTS scope_compute_incarnation uuid;
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='financial_compute_owners'::regclass AND conname='financial_compute_scope_owner_incarnation') THEN
  ALTER TABLE financial_compute_owners ADD CONSTRAINT financial_compute_scope_owner_incarnation
   CHECK(scope_compute_incarnation IS NULL OR scope_compute_incarnation=incarnation) NOT VALID;
 END IF;
END $$;

-- The environment a deployment's attempts fund: its recorded runtime owner,
-- else the project's environment of the same name. The fallback can only
-- count a deployment toward a cap, never exclude it.
CREATE OR REPLACE FUNCTION financial_compute_deployment_environment(target_deployment uuid)
RETURNS uuid LANGUAGE sql STABLE AS $$
 SELECT COALESCE(owner.environment_id, named.id)
 FROM deployments d JOIN apps a ON a.id=d.app_id
 LEFT JOIN deployment_runtime_environment_owners owner ON owner.deployment_id=d.id
 LEFT JOIN project_environments named ON owner.deployment_id IS NULL AND named.project_id=a.project_id
  AND named.slug=CASE WHEN COALESCE(NULLIF(d.scope,''),'default')='default' THEN 'production' ELSE d.scope END
 WHERE d.id=target_deployment
$$;

-- Commands of an app in a held project, or of a deployment in a held
-- environment, are held like an app's own.
CREATE OR REPLACE FUNCTION financial_app_command_held(target_app uuid, target_deployment uuid, observed_at timestamptz)
RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT financial_preview_command_held(target_app,target_deployment,observed_at) OR EXISTS(
 SELECT 1 FROM apps a JOIN financial_budget_policies p ON p.account_id=a.account_id
 JOIN financial_compute_holds h ON h.account_id=p.account_id AND h.policy_id=p.id AND h.revision=p.revision
 WHERE a.id=target_app AND p.deleted_at IS NULL AND p.spec->>'enabled'='true'
  AND p.spec->>'mode'='strict' AND (p.spec->'scope'->>'kind'='account'
    OR p.spec->'scope'->>'kind'='app' AND p.spec->'scope'->>'id'=a.id::text
    OR p.spec->'scope'->>'kind'='project' AND p.spec->'scope'->>'id'=a.project_id::text
    OR p.spec->'scope'->>'kind'='environment' AND p.spec->'scope'->>'id'=financial_compute_deployment_environment(target_deployment)::text)
  AND (p.spec->>'resume_rule'='manual' OR h.period_start=date_trunc('month',observed_at AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'))
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION financial_app_command_held(target_app uuid, target_deployment uuid, observed_at timestamptz)
RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT financial_preview_command_held(target_app,target_deployment,observed_at) OR EXISTS(
 SELECT 1 FROM apps a JOIN financial_budget_policies p ON p.account_id=a.account_id
 JOIN financial_compute_holds h ON h.account_id=p.account_id AND h.policy_id=p.id AND h.revision=p.revision
 WHERE a.id=target_app AND p.deleted_at IS NULL AND p.spec->>'enabled'='true'
  AND p.spec->>'mode'='strict' AND (p.spec->'scope'->>'kind'='account'
    OR p.spec->'scope'->>'kind'='app' AND p.spec->'scope'->>'id'=a.id::text)
  AND (p.spec->>'resume_rule'='manual' OR h.period_start=date_trunc('month',observed_at AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'))
$$;
DROP FUNCTION IF EXISTS financial_compute_deployment_environment(uuid);
ALTER TABLE financial_compute_owners DROP CONSTRAINT IF EXISTS financial_compute_scope_owner_incarnation;
ALTER TABLE financial_compute_owners DROP COLUMN IF EXISTS scope_compute_incarnation;
DROP TABLE IF EXISTS financial_compute_scope_books;
-- +goose StatementEnd
