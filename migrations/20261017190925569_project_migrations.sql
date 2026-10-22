-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
CREATE TABLE IF NOT EXISTS project_migrations (
 id uuid PRIMARY KEY,
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 project_slug text NOT NULL,
 created_at timestamptz NOT NULL,
 version bigint NOT NULL CHECK (version > 0),
 lease_until timestamptz,
 snapshot jsonb NOT NULL CHECK (jsonb_typeof(snapshot) = 'object'),
 CONSTRAINT project_migration_report_check CHECK (COALESCE(
  jsonb_typeof(snapshot->'Report') = 'object'
  AND snapshot->'Report'->>'status' IN ('prepared','applying','tracking','attention')
  AND snapshot->'Report'->>'id' = id::text
  AND snapshot->'Report'->>'project' = project_slug
  AND jsonb_array_length(snapshot->'Report'->'services') BETWEEN 1 AND 10, false))
);
CREATE INDEX IF NOT EXISTS project_migrations_account_idx ON project_migrations(account_id,created_at DESC,id DESC);
CREATE INDEX IF NOT EXISTS project_migrations_project_idx ON project_migrations(account_id,project_slug);

-- +goose Down
DROP TABLE project_migrations;
