-- +goose Up
-- +goose StatementBegin
-- filename: 20260905100000000_deployments_source_root.sql
--
-- Persist the selected build root for workspace-aware source deploys. The
-- value is repository-relative and empty/NULL means the archive root, so
-- existing deployments remain byte-compatible on the API.

ALTER TABLE deployments
    ADD COLUMN IF NOT EXISTS source_root text;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_catalog.pg_constraint
        WHERE conname = 'deployments_source_root_shape_chk'
          AND conrelid = 'deployments'::regclass
    ) THEN
        ALTER TABLE deployments
            ADD CONSTRAINT deployments_source_root_shape_chk
                CHECK (source_root IS NULL
                       OR source_root = ''
                       OR source_root = '.'
                       OR (source_root !~ '^/'
                           AND source_root !~ '(^|/)\.\.(/|$)'));
    END IF;
END$$;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE deployments
    DROP CONSTRAINT IF EXISTS deployments_source_root_shape_chk;
ALTER TABLE deployments
    DROP COLUMN IF EXISTS source_root;
-- +goose StatementEnd
