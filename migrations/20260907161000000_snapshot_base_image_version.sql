-- HTTP/2 and gRPC snapshots depend on the runner base image as well as the
-- Firecracker version. Persist the base-image generation that produced each
-- snapshot so an imaged restart can invalidate only incompatible rows.
-- Legacy rows remain the empty string and are re-primed once.
-- +goose Up
-- +goose StatementBegin

ALTER TABLE snapshots
    ADD COLUMN IF NOT EXISTS base_image_version text NOT NULL DEFAULT '';

COMMENT ON COLUMN snapshots.base_image_version IS
    'Runner base-image compatibility generation; required for HTTP/2 and gRPC snapshot restore.';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Snapshot compatibility metadata is forward-only. Keeping the column makes
-- rollback safe when rows were already written by newer imaged binaries.
SELECT 1;
-- +goose StatementEnd
