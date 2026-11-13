-- filename: 20261113200248737_environment_branch_pr_previews.sql
-- migration-compat: expand

-- ADR-20261113121342: a project chooses branch-backed PR previews
-- (preview_mode = 'branch'), and a branch made for a pull request records its
-- number so githubd only ever adopts, extends or closes its own branches. The
-- constant default keeps preview_mode metadata-only; an older apid and githubd
-- ignore both columns and keep app previews.

-- +goose Up
SET LOCAL lock_timeout = '5s';

ALTER TABLE github_deploy_policies ADD COLUMN IF NOT EXISTS preview_mode text NOT NULL DEFAULT 'app';
ALTER TABLE project_environment_branches ADD COLUMN IF NOT EXISTS pull_request_number integer;

-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_catalog.pg_constraint
        WHERE conname = 'github_deploy_policies_preview_mode_chk'
          AND conrelid = 'github_deploy_policies'::regclass
    ) THEN
        ALTER TABLE github_deploy_policies
            ADD CONSTRAINT github_deploy_policies_preview_mode_chk
            CHECK (preview_mode IN ('app', 'branch')) NOT VALID;
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM pg_catalog.pg_constraint
        WHERE conname = 'project_environment_branches_pull_request_number_chk'
          AND conrelid = 'project_environment_branches'::regclass
    ) THEN
        ALTER TABLE project_environment_branches
            ADD CONSTRAINT project_environment_branches_pull_request_number_chk
            CHECK (pull_request_number IS NULL OR pull_request_number > 0) NOT VALID;
    END IF;
END$$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';

ALTER TABLE project_environment_branches DROP CONSTRAINT IF EXISTS project_environment_branches_pull_request_number_chk;
ALTER TABLE project_environment_branches DROP COLUMN IF EXISTS pull_request_number;
ALTER TABLE github_deploy_policies DROP CONSTRAINT IF EXISTS github_deploy_policies_preview_mode_chk;
ALTER TABLE github_deploy_policies DROP COLUMN IF EXISTS preview_mode;
