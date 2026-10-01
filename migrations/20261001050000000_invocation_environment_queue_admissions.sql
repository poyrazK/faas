-- +goose Up
-- ADR-375: immutable operational queue ownership. Messages and admissions are
-- reset at clone; desired definitions remain pinned in workload settings.
CREATE TABLE invocation_environment_queue_admissions (
    invocation_id uuid PRIMARY KEY REFERENCES invocations(id) ON DELETE CASCADE,
    environment_id uuid NOT NULL REFERENCES project_environments(id),
    account_id uuid NOT NULL REFERENCES accounts(id),
    app_id uuid NOT NULL REFERENCES apps(id),
    consumer_id uuid NOT NULL REFERENCES project_environment_queue_consumers(id),
    runtime_set_id uuid NOT NULL REFERENCES project_environment_queue_runtime_sets(id),
    deployment_id uuid NOT NULL REFERENCES deployments(id),
    workload_spec_id uuid NOT NULL REFERENCES project_environment_workload_specs(id),
    pin_hash text NOT NULL CHECK (pin_hash ~ '^[a-f0-9]{64}$'),
    settings_hash text NOT NULL CHECK (settings_hash ~ '^[a-f0-9]{64}$'),
    definition_hash text NOT NULL CHECK (definition_hash ~ '^[a-f0-9]{64}$'),
    queue_name text NOT NULL CHECK (queue_name ~ '^[a-z][a-z0-9-]{0,62}$'),
    admitted_at timestamptz NOT NULL
);
CREATE INDEX invocation_environment_queue_domain_idx
    ON invocation_environment_queue_admissions(environment_id, app_id, queue_name);

-- +goose Down
DROP TABLE invocation_environment_queue_admissions;
