-- filename: 20261112193412628_environment_branch_slot.sql
-- migration-compat: expand
-- ADR-20261031193635 stable branch URLs: a branch's slot is a UUID derived
-- from its project and slug, so the branch's workload URLs survive a reset
-- (delete and create again under the same name). Rows made before this
-- carry none and keep only their environment URLs.

-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE project_environment_branches ADD COLUMN IF NOT EXISTS slot_id uuid;

-- +goose Down
SET LOCAL lock_timeout = '5s';
ALTER TABLE project_environment_branches DROP COLUMN IF EXISTS slot_id;
