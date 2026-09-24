-- +goose Up
CREATE TABLE IF NOT EXISTS project_environment_routing_policies (
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    project_id uuid NOT NULL,
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    environment_slug text NOT NULL,
    rules jsonb NOT NULL DEFAULT '[]'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (app_id, environment_slug),
    FOREIGN KEY (project_id, environment_slug)
        REFERENCES project_environments(project_id, slug) ON DELETE CASCADE,
    CONSTRAINT project_environment_routing_policies_rules_chk
        CHECK (jsonb_typeof(rules) = 'array' AND jsonb_array_length(rules) <= 20)
);

CREATE INDEX IF NOT EXISTS project_environment_routing_policies_project_idx
    ON project_environment_routing_policies (account_id, project_id, environment_slug);

-- +goose Down
DROP TABLE IF EXISTS project_environment_routing_policies;
