-- filename: 20260923220000001_deployment_release_command.sql

-- +goose Up
-- +goose StatementBegin
-- ADR-222: pin a release command to the immutable deployment that declared
-- it. A later migration/orchestrator slice consumes this contract to create
-- the unique release app task before traffic activation.
ALTER TABLE deployments
    ADD COLUMN IF NOT EXISTS release_command text[] NOT NULL DEFAULT ARRAY[]::text[],
    ADD COLUMN IF NOT EXISTS release_command_shell boolean NOT NULL DEFAULT false;

ALTER TABLE deployments
    DROP CONSTRAINT IF EXISTS deployments_release_command_chk;
ALTER TABLE deployments
    ADD CONSTRAINT deployments_release_command_chk CHECK (
        (cardinality(release_command) = 0 AND NOT release_command_shell)
        OR
        (
            cardinality(release_command) BETWEEN 1 AND 64
            AND array_position(release_command, NULL) IS NULL
            AND octet_length(btrim(release_command[1])) BETWEEN 1 AND 4096
            AND octet_length(array_to_string(release_command, '')) BETWEEN 1 AND 16384
            AND (NOT release_command_shell OR cardinality(release_command) = 1)
        )
    );
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE deployments
    DROP CONSTRAINT IF EXISTS deployments_release_command_chk;
ALTER TABLE deployments
    DROP COLUMN IF EXISTS release_command_shell,
    DROP COLUMN IF EXISTS release_command;
-- +goose StatementEnd
