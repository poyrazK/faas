-- +goose Up
CREATE TABLE IF NOT EXISTS project_environment_route_policies (
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    project_id uuid NOT NULL,
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    environment_slug text NOT NULL,
    only_allow_declared_routes boolean NOT NULL DEFAULT false,
    declared_routes jsonb NOT NULL DEFAULT '[]'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (app_id, environment_slug),
    FOREIGN KEY (project_id, environment_slug)
        REFERENCES project_environments(project_id, slug) ON DELETE CASCADE,
    CONSTRAINT project_environment_route_policies_routes_array_chk
        CHECK (jsonb_typeof(declared_routes) = 'array' AND jsonb_array_length(declared_routes) <= 50),
    CONSTRAINT project_environment_route_policies_explicit_chk
        CHECK (NOT only_allow_declared_routes OR jsonb_array_length(declared_routes) > 0)
);

CREATE INDEX IF NOT EXISTS project_environment_route_policies_project_idx
    ON project_environment_route_policies (account_id, project_id, environment_slug);

-- +goose Down
DROP TABLE IF EXISTS project_environment_route_policies;
