-- filename: 20261008190612764_build_provenance_dev_patch.sql

-- +goose Up
-- +goose StatementBegin
-- ADR-740 phase 1: the builder VM records whether a build copied its source
-- into the image unchanged (api.DevPatchSourceMap). NULL for older builds and
-- for builds whose guest predates the field.
ALTER TABLE build_provenance
    ADD COLUMN IF NOT EXISTS dev_patch jsonb
    CHECK (dev_patch IS NULL OR (jsonb_typeof(dev_patch) = 'object' AND octet_length(dev_patch::text) <= 65536));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE build_provenance DROP COLUMN IF EXISTS dev_patch;
-- +goose StatementEnd
