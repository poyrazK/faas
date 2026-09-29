-- +goose Up
-- One immutable, complete manifest is committed before any object is copied.
-- Bucket IDs are retained as historical identities after cleanup/deletion.
CREATE TABLE project_environment_clone_object_manifests (
    operation_id uuid NOT NULL REFERENCES project_environment_clone_operations(id) ON DELETE CASCADE,
    source_bucket_id uuid NOT NULL,
    target_bucket_id uuid NOT NULL,
    captured_at timestamptz NOT NULL,
    -- Preserve Go/provider timestamp precision for immutable replay checks.
    captured_at_exact text NOT NULL,
    manifest_hash text NOT NULL CHECK (manifest_hash ~ '^[a-f0-9]{64}$'),
    object_count integer NOT NULL CHECK (object_count >= 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (operation_id, source_bucket_id),
    CHECK (source_bucket_id <> target_bucket_id)
);

CREATE TABLE project_environment_clone_object_entries (
    operation_id uuid NOT NULL,
    source_bucket_id uuid NOT NULL,
    object_key text NOT NULL CHECK (length(object_key) BETWEEN 1 AND 1024),
    source_version text NOT NULL CHECK (source_version <> ''),
    -- Canonical source identity includes nanosecond timestamps. PostgreSQL's
    -- timestamptz precision would otherwise change its hash on reload.
    source_object jsonb NOT NULL,
    copied_at timestamptz,
    target_etag text NOT NULL DEFAULT '',
    verified_sha256 text NOT NULL DEFAULT '' CHECK (verified_sha256 = '' OR verified_sha256 ~ '^[a-f0-9]{64}$'),
    PRIMARY KEY (operation_id, source_bucket_id, object_key),
    FOREIGN KEY (operation_id, source_bucket_id)
        REFERENCES project_environment_clone_object_manifests(operation_id, source_bucket_id) ON DELETE CASCADE,
    CHECK (jsonb_typeof(source_object) = 'object'),
    CHECK (source_object->>'key' = object_key),
    CHECK (source_object->>'version_id' = source_version),
    CHECK ((copied_at IS NULL AND target_etag = '' AND verified_sha256 = '') OR
           (copied_at IS NOT NULL AND verified_sha256 <> ''))
);
CREATE INDEX project_environment_clone_object_uncopied_idx
    ON project_environment_clone_object_entries (operation_id, source_bucket_id, object_key)
    WHERE copied_at IS NULL;

-- +goose Down
DROP TABLE project_environment_clone_object_entries;
DROP TABLE project_environment_clone_object_manifests;
