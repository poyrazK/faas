-- filename: 20261122152641469_storage_grant_audit_platform_build_output.sql
-- migration-compat: expand
-- +goose NO TRANSACTION
-- ADR-20261117190000 amendment 1: nodes may create platform objects and
-- build outputs (SBOMs), so secretd records those write grants too.
-- Platform rows use the nil subject (they belong to no tenant). The wider
-- check replaces the old one in three steps, each its own transaction.

-- +goose Up
-- +goose StatementBegin
SET lock_timeout = '5s';
ALTER TABLE storage_grant_audit ADD CONSTRAINT storage_grant_audit_subject_class_v2_check
    CHECK (subject_class IN ('deployment', 'sandbox_account', 'job', 'build', 'build_output', 'platform')) NOT VALID;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE storage_grant_audit VALIDATE CONSTRAINT storage_grant_audit_subject_class_v2_check;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE storage_grant_audit DROP CONSTRAINT IF EXISTS storage_grant_audit_subject_class_check;
RESET lock_timeout;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET lock_timeout = '5s';
ALTER TABLE storage_grant_audit ADD CONSTRAINT storage_grant_audit_subject_class_check
    CHECK (subject_class IN ('deployment', 'sandbox_account', 'job', 'build')) NOT VALID;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE storage_grant_audit DROP CONSTRAINT IF EXISTS storage_grant_audit_subject_class_v2_check;
RESET lock_timeout;
-- +goose StatementEnd
