-- +goose Up
-- +goose StatementBegin

-- Preserve Firecracker's logical mem_bytes/disk_bytes for restore and RAM
-- compatibility checks, while recording the filesystem blocks actually used
-- by the sparse published artifacts for fleet economics.
ALTER TABLE snapshots
    ADD COLUMN IF NOT EXISTS stored_bytes bigint NOT NULL DEFAULT 0;

ALTER TABLE snapshots
    DROP CONSTRAINT IF EXISTS snapshots_stored_bytes_nonnegative;
ALTER TABLE snapshots
    ADD CONSTRAINT snapshots_stored_bytes_nonnegative CHECK (stored_bytes >= 0);

COMMENT ON COLUMN snapshots.stored_bytes IS
    'Filesystem allocation of published mem + vmstate artifacts. Zero means a legacy writer; telemetry conservatively falls back to logical bytes.';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE snapshots DROP CONSTRAINT IF EXISTS snapshots_stored_bytes_nonnegative;
ALTER TABLE snapshots DROP COLUMN IF EXISTS stored_bytes;
-- +goose StatementEnd
