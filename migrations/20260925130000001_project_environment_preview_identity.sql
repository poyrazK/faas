-- +goose Up
ALTER TABLE project_environments
    ADD COLUMN IF NOT EXISTS preview_pr_number integer,
    ADD COLUMN IF NOT EXISTS preview_head_sha text;

ALTER TABLE project_environments
    ADD CONSTRAINT project_environments_preview_identity_chk
    CHECK (
        (preview_pr_number IS NULL AND preview_head_sha IS NULL)
        OR (NOT protected AND preview_pr_number IS NOT NULL AND preview_pr_number > 0
            AND preview_head_sha IS NOT NULL
            AND preview_head_sha ~ '^[0-9a-f]{40}$')
    );

CREATE UNIQUE INDEX IF NOT EXISTS project_environments_preview_pr_uniq
    ON project_environments (project_id, preview_pr_number)
    WHERE preview_pr_number IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS project_environments_preview_pr_uniq;
ALTER TABLE project_environments
    DROP CONSTRAINT IF EXISTS project_environments_preview_identity_chk,
    DROP COLUMN IF EXISTS preview_head_sha,
    DROP COLUMN IF EXISTS preview_pr_number;
