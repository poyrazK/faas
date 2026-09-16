-- filename: 20260916201527599_project_environment_approval_lifecycle.sql

-- +goose Up
-- +goose StatementBegin
ALTER TABLE project_environment_approvals
    ADD COLUMN IF NOT EXISTS token_kind text NOT NULL DEFAULT 'plan',
    ADD COLUMN IF NOT EXISTS consumed_at timestamptz;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
          FROM pg_constraint
         WHERE conname = 'project_environment_approvals_token_kind_check'
           AND conrelid = 'project_environment_approvals'::regclass
    ) THEN
        ALTER TABLE project_environment_approvals
            ADD CONSTRAINT project_environment_approvals_token_kind_check
            CHECK (token_kind IN ('plan', 'promotion'));
    END IF;
END
$$;

CREATE INDEX IF NOT EXISTS project_environment_approvals_id_lookup_idx
    ON project_environment_approvals (account_id, project_slug, environment_slug, id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS project_environment_approvals_id_lookup_idx;
ALTER TABLE project_environment_approvals
    DROP CONSTRAINT IF EXISTS project_environment_approvals_token_kind_check;
ALTER TABLE project_environment_approvals
    DROP COLUMN IF EXISTS consumed_at,
    DROP COLUMN IF EXISTS token_kind;
-- +goose StatementEnd
