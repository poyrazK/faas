-- +goose Up
ALTER TABLE custom_domains
    ADD COLUMN environment_id uuid REFERENCES project_environments(id) ON DELETE RESTRICT;

CREATE INDEX custom_domains_environment_id_idx
    ON custom_domains (environment_id) WHERE environment_id IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS custom_domains_environment_id_idx;
ALTER TABLE custom_domains DROP COLUMN IF EXISTS environment_id;
