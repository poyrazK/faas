-- filename: 20260928221259822_project_environment_qualification_secret_revisions.sql

-- +goose Up
ALTER TABLE project_environment_qualifications
    ADD COLUMN IF NOT EXISTS secret_revision_hashes jsonb NOT NULL DEFAULT '{}'::jsonb;

-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_catalog.pg_constraint
        WHERE conname = 'project_environment_qualifications_secret_revisions_object_check'
          AND conrelid = 'project_environment_qualifications'::regclass
    ) THEN
        ALTER TABLE project_environment_qualifications
            ADD CONSTRAINT project_environment_qualifications_secret_revisions_object_check
            CHECK (jsonb_typeof(secret_revision_hashes) = 'object');
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
ALTER TABLE project_environment_qualifications
    DROP CONSTRAINT IF EXISTS project_environment_qualifications_secret_revisions_object_check,
    DROP COLUMN IF EXISTS secret_revision_hashes;
