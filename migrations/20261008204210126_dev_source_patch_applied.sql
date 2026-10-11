-- filename: 20261008204210126_dev_source_patch_applied.sql

-- +goose Up
-- +goose StatementBegin
-- ADR-740 phase 3: vmmd records the first instance acknowledgement of a
-- developer live patch so `gregale dev` can report when the edit reached the
-- running environment. apply_error is a bounded guest error code; a NULL
-- applied_at means no instance has acknowledged the patch yet.
ALTER TABLE dev_source_patches
    ADD COLUMN IF NOT EXISTS applied_at timestamptz,
    ADD COLUMN IF NOT EXISTS apply_ms integer CHECK (apply_ms IS NULL OR (apply_ms >= 0 AND apply_ms <= 600000)),
    ADD COLUMN IF NOT EXISTS apply_error text CHECK (apply_error IS NULL OR apply_error ~ '^[a-z][a-z0-9_]{0,63}$');
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE dev_source_patches
    DROP COLUMN IF EXISTS apply_error,
    DROP COLUMN IF EXISTS apply_ms,
    DROP COLUMN IF EXISTS applied_at;
-- +goose StatementEnd
