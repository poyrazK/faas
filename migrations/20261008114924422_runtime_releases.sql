-- filename: 20261008114924422_runtime_releases.sql

-- +goose Up
ALTER TABLE build_provenance ADD COLUMN runtime_base_ref text NOT NULL DEFAULT '';
CREATE TABLE runtime_releases (
 id text PRIMARY KEY CHECK (id ~ '^[a-f0-9]{64}$'),
 runtime text NOT NULL CHECK (runtime IN ('node22','node24','python312','python313','go124','go124-alpine')),
 architecture text NOT NULL CHECK (architecture IN ('amd64','arm64')),
 source_ref text NOT NULL CHECK (source_ref ~ '@sha256:[a-f0-9]{64}$'),
 guest_init_sha256 text NOT NULL CHECK (guest_init_sha256 ~ '^[a-f0-9]{64}$'),
 layout_version text NOT NULL CHECK (length(layout_version) BETWEEN 1 AND 64),
 base_sha256 text NOT NULL CHECK (base_sha256 ~ '^[a-f0-9]{64}$'),
 created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE (runtime, architecture, source_ref, guest_init_sha256, layout_version)
);
CREATE TABLE runtime_artifact_bindings (
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 rootfs_key text NOT NULL CHECK (length(rootfs_key) BETWEEN 1 AND 1024),
 release_id text NOT NULL REFERENCES runtime_releases(id),
 PRIMARY KEY (account_id, rootfs_key)
);
CREATE INDEX runtime_releases_catalog_idx ON runtime_releases (runtime, architecture, created_at DESC, id);

-- +goose Down
-- Forward-only: preserve the base identity needed to boot existing artifacts.
SELECT 1;
