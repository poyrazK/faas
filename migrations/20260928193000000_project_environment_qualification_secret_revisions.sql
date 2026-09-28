-- filename: 20260928193000000_project_environment_qualification_secret_revisions.sql

-- +goose Up
ALTER TABLE project_environment_qualifications
    ADD COLUMN secret_revision_hashes jsonb NOT NULL DEFAULT '{}'::jsonb;

ALTER TABLE project_environment_qualifications
    ADD CONSTRAINT project_environment_qualifications_secret_revisions_object_check
    CHECK (jsonb_typeof(secret_revision_hashes) = 'object');

-- +goose Down
ALTER TABLE project_environment_qualifications
    DROP CONSTRAINT IF EXISTS project_environment_qualifications_secret_revisions_object_check,
    DROP COLUMN IF EXISTS secret_revision_hashes;
