-- +goose Up
-- ADR-375: bind the source revision to the frozen catalogue in the creation tx.
ALTER TABLE project_environment_clone_operations ADD COLUMN configuration_capture_version integer NOT NULL DEFAULT 0 CHECK (configuration_capture_version IN (0,1));
CREATE TABLE project_environment_clone_configuration_captures (
    operation_id uuid PRIMARY KEY REFERENCES project_environment_clone_operations(id) ON DELETE CASCADE,
    version integer NOT NULL CHECK (version=1),
    configuration_hash text NOT NULL CHECK (configuration_hash ~ '^[a-f0-9]{64}$'),
    configuration json NOT NULL CHECK (json_typeof(configuration)='object')
);

-- +goose Down
DROP TABLE project_environment_clone_configuration_captures;
ALTER TABLE project_environment_clone_operations DROP COLUMN configuration_capture_version;
