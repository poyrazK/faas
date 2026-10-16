-- +goose Up
CREATE TABLE IF NOT EXISTS project_release_stages (
 id uuid PRIMARY KEY,
 account_id uuid NOT NULL REFERENCES accounts(id),
 project_id uuid NOT NULL REFERENCES projects(id),
 environment_slug text NOT NULL CHECK (environment_slug ~ '^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$'),
 manifest jsonb NOT NULL CHECK (jsonb_typeof(manifest)='object'),
 targets jsonb NOT NULL CHECK (jsonb_typeof(targets)='object' AND targets <> '{}'::jsonb),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 expires_at timestamptz NOT NULL CHECK (expires_at>created_at AND expires_at<=created_at+interval '1800 seconds'),
 cancelled boolean NOT NULL DEFAULT false,
 FOREIGN KEY (project_id,environment_slug) REFERENCES project_environments(project_id,slug)
);
CREATE INDEX IF NOT EXISTS project_release_stages_scope ON project_release_stages(project_id,environment_slug,expires_at);
-- +goose Down
DROP TABLE project_release_stages;
