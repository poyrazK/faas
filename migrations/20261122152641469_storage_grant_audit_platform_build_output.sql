-- filename: 20261122152641469_storage_grant_audit_platform_build_output.sql
-- migration-compat: expand
-- ADR-20261117190000 amendment 1: nodes may create platform objects and
-- build outputs (SBOMs), so secretd records those write grants too.
-- Platform rows use the nil subject (they belong to no tenant).

-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE storage_grant_audit DROP CONSTRAINT IF EXISTS storage_grant_audit_subject_class_check;
ALTER TABLE storage_grant_audit ADD CONSTRAINT storage_grant_audit_subject_class_check
    CHECK (subject_class IN ('deployment', 'sandbox_account', 'job', 'build', 'build_output', 'platform')) NOT VALID;
ALTER TABLE storage_grant_audit VALIDATE CONSTRAINT storage_grant_audit_subject_class_check;

-- +goose Down
SET LOCAL lock_timeout = '5s';
ALTER TABLE storage_grant_audit DROP CONSTRAINT IF EXISTS storage_grant_audit_subject_class_check;
ALTER TABLE storage_grant_audit ADD CONSTRAINT storage_grant_audit_subject_class_check
    CHECK (subject_class IN ('deployment', 'sandbox_account', 'job', 'build')) NOT VALID;
