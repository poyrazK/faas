-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS project_environments (
    id         uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id uuid        NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    project_id uuid        NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    slug       text        NOT NULL,
    protected  boolean     NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT project_environments_slug_shape CHECK (
        slug ~ '^[a-z0-9]([a-z0-9-]{0,31}[a-z0-9])?$'
    ),
    CONSTRAINT project_environments_project_slug_uniq UNIQUE (project_id, slug)
);

CREATE INDEX IF NOT EXISTS project_environments_account_project_idx
    ON project_environments (account_id, project_id, slug);

-- Every existing project gets an explicit, protected production target.
INSERT INTO project_environments (account_id, project_id, slug, protected)
SELECT account_id, id, 'production', true
  FROM projects
ON CONFLICT (project_id, slug) DO NOTHING;
-- +goose StatementEnd

-- +goose Down
DROP TABLE IF EXISTS project_environments;
