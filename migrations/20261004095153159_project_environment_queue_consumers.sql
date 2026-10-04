-- +goose Up
-- ADR-531: a complete, deployment-pinned queue projection. Preparation does
-- not enable delivery; message ownership and dispatch activation follow later.
CREATE TABLE project_environment_queue_runtime_sets (
    id uuid PRIMARY KEY,
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    project_id uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    environment_id uuid NOT NULL REFERENCES project_environments(id) ON DELETE CASCADE,
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    deployment_id uuid NOT NULL UNIQUE REFERENCES deployments(id) ON DELETE CASCADE,
    workload_spec_id uuid NOT NULL REFERENCES project_environment_workload_specs(id) ON DELETE CASCADE,
    settings_hash text NOT NULL CHECK (settings_hash ~ '^[a-f0-9]{64}$'),
    queue_revision bigint NOT NULL CHECK (queue_revision > 0),
    binding_count integer NOT NULL CHECK (binding_count >= 0),
    book_hash text NOT NULL CHECK (book_hash ~ '^[a-f0-9]{64}$'),
    state text NOT NULL DEFAULT 'prepared' CHECK (state = 'prepared'),
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX project_environment_queue_runtime_environment_idx
    ON project_environment_queue_runtime_sets(environment_id, app_id);

CREATE TABLE project_environment_queue_consumers (
    id uuid PRIMARY KEY,
    runtime_set_id uuid NOT NULL REFERENCES project_environment_queue_runtime_sets(id) ON DELETE CASCADE,
    name text NOT NULL CHECK (name ~ '^[a-z][a-z0-9-]{0,62}$'),
    queue_name text NOT NULL CHECK (queue_name ~ '^[a-z][a-z0-9-]{0,62}$'),
    definition json NOT NULL CHECK (json_typeof(definition) = 'object'
        AND definition->>'name' IS NOT NULL AND definition->>'name' = name
        AND definition->>'queue_name' IS NOT NULL AND definition->>'queue_name' = queue_name),
    definition_hash text NOT NULL CHECK (definition_hash ~ '^[a-f0-9]{64}$'),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (runtime_set_id, name),
    UNIQUE (runtime_set_id, queue_name)
);

-- +goose Down
DROP TABLE project_environment_queue_consumers;
DROP TABLE project_environment_queue_runtime_sets;
