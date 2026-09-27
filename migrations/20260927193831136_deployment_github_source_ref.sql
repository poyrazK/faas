-- +goose Up
ALTER TABLE deployments
    ADD COLUMN IF NOT EXISTS github_source_ref text,
    ADD COLUMN IF NOT EXISTS github_installation_id bigint;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_catalog.pg_constraint
        WHERE conname = 'deployments_github_source_ref_pair_chk'
          AND conrelid = 'deployments'::regclass
    ) THEN
        ALTER TABLE deployments
            ADD CONSTRAINT deployments_github_source_ref_pair_chk CHECK (
                (github_source_ref IS NULL AND github_installation_id IS NULL)
                OR
                (github_source_ref IS NOT NULL AND btrim(github_source_ref) <> ''
                 AND github_installation_id IS NOT NULL AND github_installation_id > 0)
            );
    END IF;
END$$;

-- +goose Down
ALTER TABLE deployments
    DROP CONSTRAINT IF EXISTS deployments_github_source_ref_pair_chk;
ALTER TABLE deployments
    DROP COLUMN IF EXISTS github_installation_id;
ALTER TABLE deployments
    DROP COLUMN IF EXISTS github_source_ref;
