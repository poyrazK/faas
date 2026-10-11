-- +goose Up
CREATE TABLE IF NOT EXISTS project_environment_workload_specs (
    id uuid PRIMARY KEY,
    environment_id uuid NOT NULL REFERENCES project_environments(id) ON DELETE CASCADE,
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    revision bigint NOT NULL CHECK (revision > 0),
    config_hash text NOT NULL CHECK (config_hash ~ '^[a-f0-9]{64}$'),
    -- JSON preserves exact nested RawMessage values for the revision hash.
    settings json NOT NULL CHECK (json_typeof(settings) = 'object'),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (environment_id, app_id, revision),
    UNIQUE (environment_id, app_id, id)
);

CREATE TABLE IF NOT EXISTS project_environment_workload_heads (
    environment_id uuid NOT NULL,
    app_id uuid NOT NULL,
    spec_id uuid NOT NULL,
    PRIMARY KEY (environment_id, app_id),
    FOREIGN KEY (environment_id, app_id, spec_id)
        REFERENCES project_environment_workload_specs(environment_id, app_id, id) ON DELETE CASCADE
);

-- Runtime consumers already invalidate app configuration on this channel.
-- New deployments pin the settings they were built and tested with.
CREATE TABLE IF NOT EXISTS project_environment_workload_deployment_specs (
    deployment_id uuid PRIMARY KEY REFERENCES deployments(id) ON DELETE CASCADE,
    spec_id uuid NOT NULL REFERENCES project_environment_workload_specs(id) ON DELETE CASCADE
);

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION notify_project_environment_workload_head() RETURNS trigger AS $$
BEGIN
    PERFORM pg_notify('app_changed', NEW.app_id::text);
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd
CREATE OR REPLACE TRIGGER project_environment_workload_head_changed
AFTER INSERT OR UPDATE ON project_environment_workload_heads
FOR EACH ROW EXECUTE FUNCTION notify_project_environment_workload_head();

-- +goose Down
DROP TABLE project_environment_workload_deployment_specs;
DROP TABLE project_environment_workload_heads;
DROP FUNCTION notify_project_environment_workload_head();
DROP TABLE project_environment_workload_specs;
