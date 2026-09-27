-- +goose Up
ALTER TABLE custom_domains
    ADD COLUMN IF NOT EXISTS environment_id uuid
        REFERENCES project_environments(id) ON DELETE CASCADE;

CREATE INDEX IF NOT EXISTS custom_domains_environment_app_idx
    ON custom_domains (environment_id, app_id)
    WHERE environment_id IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS custom_domains_environment_app_idx;
ALTER TABLE custom_domains DROP COLUMN IF EXISTS environment_id;
