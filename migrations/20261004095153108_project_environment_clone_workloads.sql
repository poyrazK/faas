-- +goose Up
-- ADR-531: immutable source artifacts and effective deployed settings are
-- captured before provider copying. Target attachment is atomic with creation.
ALTER TABLE project_environment_clone_operations ADD COLUMN IF NOT EXISTS target_release_set_id uuid;
CREATE TABLE IF NOT EXISTS project_environment_clone_workloads (
    operation_id uuid NOT NULL REFERENCES project_environment_clone_operations(id) ON DELETE CASCADE,
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    source_deployment_id uuid NOT NULL,
    source_hash text NOT NULL CHECK (source_hash ~ '^[a-f0-9]{64}$'),
    snapshot json NOT NULL CHECK (json_typeof(snapshot) = 'object'),
    target_deployment_id uuid,
    target_settings_hash text NOT NULL DEFAULT '' CHECK (target_settings_hash = '' OR target_settings_hash ~ '^[a-f0-9]{64}$'),
    PRIMARY KEY (operation_id, app_id),
    CHECK (source_deployment_id <> target_deployment_id),
    CHECK ((target_deployment_id IS NULL AND target_settings_hash = '') OR
           (target_deployment_id IS NOT NULL AND target_settings_hash <> ''))
);

-- +goose Down
DROP TABLE project_environment_clone_workloads;
ALTER TABLE project_environment_clone_operations DROP COLUMN target_release_set_id;
