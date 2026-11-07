-- filename: 20261105105936402_gcs_generation_version_references.sql
-- migration-compat: expand

-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
-- ADR-20261107123000 (amends ADR-628): a GCS generation names every object,
-- versioned bucket or not, so a non-null native version identity is no longer
-- by itself evidence of retained versions. Drop the CHECK that forced
-- versions_observed for every non-null identity. The previous release still
-- writes true, which this schema keeps accepting.
ALTER TABLE object_version_references DROP CONSTRAINT IF EXISTS object_version_references_versions_observed_check;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
-- NOT VALID: generation identities recorded since the Up may have
-- versions_observed false; the constraint guards new rows only.
ALTER TABLE object_version_references ADD CONSTRAINT object_version_references_versions_observed_check
    CHECK (native_version_id = 'null' OR versions_observed) NOT VALID;
-- +goose StatementEnd
