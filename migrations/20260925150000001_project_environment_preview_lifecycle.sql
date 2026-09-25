-- +goose Up
ALTER TABLE project_environments
    ADD COLUMN IF NOT EXISTS preview_state text,
    ADD COLUMN IF NOT EXISTS preview_expires_at timestamptz;

-- Preview environments created before lifecycle tracking was deployed get a
-- fresh bounded lifetime. This avoids making old previews immediately expire
-- during migration while ensuring none remain immortal.
UPDATE project_environments
   SET preview_state = coalesce(preview_state, 'open'),
       preview_expires_at = coalesce(preview_expires_at, now() + interval '7 days')
 WHERE preview_pr_number IS NOT NULL;

ALTER TABLE project_environments
    ADD CONSTRAINT project_environments_preview_lifecycle_chk
    CHECK (
        (preview_pr_number IS NULL AND preview_state IS NULL AND preview_expires_at IS NULL)
        OR (preview_pr_number IS NOT NULL
            AND preview_state IS NOT NULL
            AND preview_state IN ('open', 'closed', 'tearing_down')
            AND preview_expires_at IS NOT NULL)
    );

CREATE INDEX IF NOT EXISTS project_environments_preview_expiry_idx
    ON project_environments (preview_expires_at, id)
    WHERE preview_pr_number IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS project_environments_preview_expiry_idx;
ALTER TABLE project_environments
    DROP CONSTRAINT IF EXISTS project_environments_preview_lifecycle_chk,
    DROP COLUMN IF EXISTS preview_expires_at,
    DROP COLUMN IF EXISTS preview_state;
