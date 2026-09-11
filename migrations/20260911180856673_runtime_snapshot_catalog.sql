-- filename: 20260911180856673_runtime_snapshot_catalog.sql

-- +goose Up
-- +goose StatementBegin
-- Runtime snapshots are immutable, trusted platform artifacts. The catalog
-- stores compatibility identity separately from the object key so a storage
-- relocation never changes which machine shape a scheduler may restore.
CREATE TABLE IF NOT EXISTS runtime_snapshots (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    catalog_key text NOT NULL UNIQUE,
    runtime text NOT NULL,
    architecture text NOT NULL,
    kernel_digest text NOT NULL,
    guest_executor_digest text NOT NULL,
    base_image_digest text NOT NULL,
    memory_mb integer NOT NULL,
    ephemeral_disk_mb integer NOT NULL,
    format_version integer NOT NULL,
    storage_key text NOT NULL,
    snapshot_digest text NOT NULL,
    mem_bytes bigint NOT NULL,
    vm_state_bytes bigint NOT NULL,
    sanitized boolean NOT NULL,
    payload_free boolean NOT NULL,
    state text NOT NULL DEFAULT 'ready',
    created_at timestamptz NOT NULL,
    published_at timestamptz NOT NULL DEFAULT now(),
    retired_at timestamptz,
    CONSTRAINT runtime_snapshots_runtime_check CHECK (
        runtime IN ('node22', 'node24', 'python312', 'python313')
    ),
    CONSTRAINT runtime_snapshots_architecture_check CHECK (architecture IN ('amd64', 'arm64')),
    CONSTRAINT runtime_snapshots_kernel_digest_check CHECK (kernel_digest ~ '^[0-9a-f]{64}$'),
    CONSTRAINT runtime_snapshots_executor_digest_check CHECK (guest_executor_digest ~ '^[0-9a-f]{64}$'),
    CONSTRAINT runtime_snapshots_base_digest_check CHECK (base_image_digest ~ '^[0-9a-f]{64}$'),
    CONSTRAINT runtime_snapshots_memory_check CHECK (memory_mb IN (128, 256, 512, 1024)),
    CONSTRAINT runtime_snapshots_disk_check CHECK (
        ephemeral_disk_mb IN (64, 128, 256, 512, 1024, 2048)
    ),
    CONSTRAINT runtime_snapshots_format_check CHECK (format_version > 0),
    CONSTRAINT runtime_snapshots_digest_check CHECK (snapshot_digest ~ '^[0-9a-f]{64}$'),
    CONSTRAINT runtime_snapshots_sizes_check CHECK (mem_bytes > 0 AND vm_state_bytes > 0),
    CONSTRAINT runtime_snapshots_sanitized_check CHECK (sanitized AND payload_free),
    CONSTRAINT runtime_snapshots_state_check CHECK (state IN ('ready', 'retired')),
    CONSTRAINT runtime_snapshots_retirement_check CHECK (
        (state = 'ready' AND retired_at IS NULL)
        OR (state = 'retired' AND retired_at IS NOT NULL)
    ),
    CONSTRAINT runtime_snapshots_publication_order_check CHECK (published_at >= created_at)
);

CREATE INDEX IF NOT EXISTS runtime_snapshots_state_created_idx
    ON runtime_snapshots (state, created_at DESC, id DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS runtime_snapshots;
-- +goose StatementEnd
