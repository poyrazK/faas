-- filename: 20261021082348668_financial_compute_project_books.sql
-- migration-compat: expand

-- +goose Up
-- The foreign key locks accounts briefly.
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
-- ADR-1750, amendment 1: a strict project cap is a shared gross book that
-- every app in the project funds alongside its own book. Leases record the
-- project they fund in their payload (project_id).
CREATE TABLE IF NOT EXISTS financial_compute_project_books (
  account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  project_id uuid NOT NULL,
  period_start timestamptz NOT NULL,
  period_end timestamptz NOT NULL CHECK (period_end > period_start),
  payload jsonb NOT NULL CHECK (jsonb_typeof(payload) = 'object'),
  CHECK (COALESCE((payload->>'committed')::numeric >= 0 AND (payload->>'reserved')::numeric >= 0
    AND (payload->>'committed')::numeric + (payload->>'reserved')::numeric <= 9007199254740991, false)),
  PRIMARY KEY (account_id, project_id, period_start)
);

-- Commands of an app in a held project are held like an app's own.
CREATE OR REPLACE FUNCTION financial_app_command_held(target_app uuid, target_deployment uuid, observed_at timestamptz)
RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT financial_preview_command_held(target_app,target_deployment,observed_at) OR EXISTS(
 SELECT 1 FROM apps a JOIN financial_budget_policies p ON p.account_id=a.account_id
 JOIN financial_compute_holds h ON h.account_id=p.account_id AND h.policy_id=p.id AND h.revision=p.revision
 WHERE a.id=target_app AND p.deleted_at IS NULL AND p.spec->>'enabled'='true'
  AND p.spec->>'mode'='strict' AND (p.spec->'scope'->>'kind'='account'
    OR p.spec->'scope'->>'kind'='app' AND p.spec->'scope'->>'id'=a.id::text
    OR p.spec->'scope'->>'kind'='project' AND p.spec->'scope'->>'id'=a.project_id::text)
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
DROP TABLE IF EXISTS financial_compute_project_books;
-- +goose StatementEnd
