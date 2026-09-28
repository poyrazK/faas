-- filename: 20260928221258962_project_environment_qualification_config_identity.sql

-- +goose Up
ALTER TABLE project_environment_qualifications
    ADD COLUMN configuration_version bigint NOT NULL DEFAULT -1,
    ADD COLUMN configuration_hash text NOT NULL DEFAULT '';

ALTER TABLE project_environment_qualifications
    ADD CONSTRAINT project_environment_qualifications_configuration_identity_check
    CHECK (
        (configuration_version = -1 AND configuration_hash = '')
        OR (configuration_version >= 0 AND configuration_hash ~ '^[a-f0-9]{64}$')
    );

-- +goose Down
ALTER TABLE project_environment_qualifications
    DROP CONSTRAINT IF EXISTS project_environment_qualifications_configuration_identity_check,
    DROP COLUMN IF EXISTS configuration_version,
    DROP COLUMN IF EXISTS configuration_hash;
