-- +goose Up
CREATE TABLE project_environment_promotion_workload_specs (
    promotion_id uuid NOT NULL REFERENCES project_environment_promotions(id) ON DELETE CASCADE,
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    deployment_id uuid NOT NULL UNIQUE REFERENCES deployments(id) ON DELETE CASCADE,
    prepared_spec_id uuid NOT NULL REFERENCES project_environment_workload_specs(id) ON DELETE CASCADE,
    previous_spec_id uuid REFERENCES project_environment_workload_specs(id) ON DELETE SET NULL,
    source_hash text NOT NULL CHECK (source_hash ~ '^[a-f0-9]{64}$'),
    previous_hash text NOT NULL CHECK (previous_hash ~ '^[a-f0-9]{64}$'),
    previous_settings json NOT NULL CHECK (json_typeof(previous_settings) = 'object'),
    PRIMARY KEY (promotion_id, app_id)
);

-- +goose Down
DROP TABLE project_environment_promotion_workload_specs;
