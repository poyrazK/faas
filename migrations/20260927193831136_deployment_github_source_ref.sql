-- +goose Up
ALTER TABLE deployments
    ADD COLUMN github_source_ref text,
    ADD COLUMN github_installation_id bigint,
    ADD CONSTRAINT deployments_github_source_ref_pair_chk CHECK (
        (github_source_ref IS NULL AND github_installation_id IS NULL)
        OR
        (github_source_ref IS NOT NULL AND btrim(github_source_ref) <> ''
         AND github_installation_id IS NOT NULL AND github_installation_id > 0)
    );

-- +goose Down
ALTER TABLE deployments
    DROP CONSTRAINT deployments_github_source_ref_pair_chk,
    DROP COLUMN github_installation_id,
    DROP COLUMN github_source_ref;
