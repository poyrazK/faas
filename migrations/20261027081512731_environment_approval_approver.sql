-- filename: 20261027081512731_environment_approval_approver.sql
-- migration-compat: expand
-- ADR-20261020085706 amendment 2: a protected environment's promotion
-- approval stands in for an app's release approval policy, which may forbid
-- self-approval. The approval now records who gave it, so a promotion can
-- refuse one its own promoter approved. NULL for approvals made before this
-- column, which expire within the approval lifetime.
--
-- The previous release neither reads nor writes the column.

-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE project_environment_approvals ADD COLUMN IF NOT EXISTS approved_by_account_id uuid;

-- +goose Down
SET LOCAL lock_timeout = '5s';
ALTER TABLE project_environment_approvals DROP COLUMN IF EXISTS approved_by_account_id;
