-- filename: 20261106154818002_environment_approval_principal.sql
-- migration-compat: expand
-- Bug hunt H70 (ADR-1242 amendment 2): a protected environment's approval
-- records the credential that gave it, a browser session or an interactive
-- sign-in key ("session:<id>" or "key:<id>"), beside the approving account,
-- so the audit trail names the person's sign-in rather than only the account.
-- NULL for approvals made before this column, which expire within the
-- approval lifetime.
--
-- The previous release neither reads nor writes the column.

-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE project_environment_approvals ADD COLUMN IF NOT EXISTS approved_by_principal text;

-- +goose Down
SET LOCAL lock_timeout = '5s';
ALTER TABLE project_environment_approvals DROP COLUMN IF EXISTS approved_by_principal;
