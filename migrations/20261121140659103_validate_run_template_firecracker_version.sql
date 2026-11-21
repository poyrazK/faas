-- filename: 20261121140659103_validate_run_template_firecracker_version.sql
-- migration-compat: expand

-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE runtime_snapshots VALIDATE CONSTRAINT runtime_snapshots_fc_version_check;

-- +goose Down
-- Validation does not change accepted writes and is intentionally retained.
SELECT 1;
