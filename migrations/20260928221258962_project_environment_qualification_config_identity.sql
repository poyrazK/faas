-- filename: 20260928221258962_project_environment_qualification_config_identity.sql

-- +goose Up
ALTER TABLE project_environment_qualifications
    ADD COLUMN IF NOT EXISTS configuration_version bigint NOT NULL DEFAULT -1,
    ADD COLUMN IF NOT EXISTS configuration_hash text NOT NULL DEFAULT '';

-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_catalog.pg_constraint
        WHERE conname = 'project_environment_qualifications_configuration_identity_check'
          AND conrelid = 'project_environment_qualifications'::regclass
    ) THEN
        ALTER TABLE project_environment_qualifications
            ADD CONSTRAINT project_environment_qualifications_configuration_identity_check
            CHECK (
                (configuration_version = -1 AND configuration_hash = '')
                OR (configuration_version >= 0 AND configuration_hash ~ '^[a-f0-9]{64}$')
            );
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
ALTER TABLE project_environment_qualifications
    DROP CONSTRAINT IF EXISTS project_environment_qualifications_configuration_identity_check,
    DROP COLUMN IF EXISTS configuration_version,
    DROP COLUMN IF EXISTS configuration_hash;
