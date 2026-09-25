-- +goose Up
-- An immutable project/environment deployment graph. The active pointer is
-- switched only after every member has been validated and inserted.
CREATE TABLE IF NOT EXISTS project_release_sets (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    project_id uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    environment_slug text NOT NULL,
    active boolean NOT NULL DEFAULT false,
    ttl_seconds integer NOT NULL CHECK (ttl_seconds BETWEEN 1 AND 604800),
    expires_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (project_id, environment_slug)
        REFERENCES project_environments(project_id, slug) ON DELETE CASCADE,
    CONSTRAINT project_release_set_expiry_state CHECK (
        (active AND expires_at IS NULL) OR (NOT active AND expires_at IS NOT NULL))
);
CREATE UNIQUE INDEX IF NOT EXISTS project_release_sets_active_uniq
    ON project_release_sets (project_id, environment_slug) WHERE active;
CREATE INDEX IF NOT EXISTS project_release_sets_expiry_idx ON project_release_sets (expires_at);

CREATE TABLE IF NOT EXISTS project_release_members (
    release_id uuid NOT NULL REFERENCES project_release_sets(id) ON DELETE CASCADE,
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    deployment_id uuid NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
    PRIMARY KEY (release_id, app_id)
);
CREATE INDEX IF NOT EXISTS project_release_members_deployment_idx
    ON project_release_members (deployment_id, release_id);

-- +goose Down
DROP TABLE IF EXISTS project_release_members;
DROP TABLE IF EXISTS project_release_sets;
