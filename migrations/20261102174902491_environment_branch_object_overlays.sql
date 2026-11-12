-- filename: 20261102174902491_environment_branch_object_overlays.sql
-- migration-compat: expand

-- +goose Up
-- The foreign keys lock object_buckets and accounts briefly.
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
-- ADR-20261031193635 slice 3: an environment branch's bucket is an ordinary
-- bucket in the branch's scope with an overlay on its source bucket. The S3
-- gateway serves a key the branch never wrote, and has not deleted, from the
-- source. New tables, so an older gateway and apid keep working.
CREATE TABLE IF NOT EXISTS object_bucket_overlays (
    bucket_id        uuid        PRIMARY KEY REFERENCES object_buckets(id) ON DELETE CASCADE,
    account_id       uuid        NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    -- No foreign key: a deleted source leaves the overlay with nothing to
    -- read through to.
    source_bucket_id uuid        NOT NULL,
    source_app_id    uuid        NOT NULL,
    -- live_parent reads the source's current object; as_of (later) reads the
    -- version current at as_of from a versioned source.
    consistency      text        NOT NULL DEFAULT 'live_parent',
    as_of            timestamptz NOT NULL,
    created_at       timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT object_bucket_overlays_not_self CHECK (source_bucket_id <> bucket_id),
    CONSTRAINT object_bucket_overlays_consistency CHECK (consistency IN ('live_parent', 'as_of'))
);
COMMENT ON TABLE object_bucket_overlays IS
    'ADR-20261031193635 environment branch buckets that read through to their source bucket for keys they never wrote.';

-- A key deleted in the branch hides the source's object.
CREATE TABLE IF NOT EXISTS object_bucket_overlay_tombstones (
    bucket_id  uuid        NOT NULL REFERENCES object_bucket_overlays(bucket_id) ON DELETE CASCADE,
    key        text        NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (bucket_id, key),
    CONSTRAINT object_bucket_overlay_tombstones_key CHECK (octet_length(key) BETWEEN 1 AND 1024)
);
COMMENT ON TABLE object_bucket_overlay_tombstones IS
    'ADR-20261031193635 keys an environment branch deleted, hiding its source bucket''s objects.';

-- One row per bucket a branch owns, written before the bucket is reserved and
-- kept, like project_environment_branch_databases, until apid's branch data
-- reconciler has purged and deleted the bucket after the branch is gone.
CREATE TABLE IF NOT EXISTS project_environment_branch_buckets (
    id               uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id       uuid        NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    project_id       uuid        NOT NULL,
    environment_id   uuid        NOT NULL,
    environment_slug text        NOT NULL,
    app_id           uuid        NOT NULL,
    source_bucket_id uuid        NOT NULL,
    copy_bucket_id   uuid,
    lease_token      uuid,
    lease_until      timestamptz,
    created_at       timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT project_environment_branch_buckets_one_copy UNIQUE (environment_id, app_id, source_bucket_id)
);
CREATE INDEX IF NOT EXISTS project_environment_branch_buckets_environment_idx
    ON project_environment_branch_buckets (environment_id);
COMMENT ON TABLE project_environment_branch_buckets IS
    'ADR-20261031193635 overlay buckets owned by an environment branch; purged and deleted after the branch is gone.';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS project_environment_branch_buckets;
DROP TABLE IF EXISTS object_bucket_overlay_tombstones;
DROP TABLE IF EXISTS object_bucket_overlays;
-- +goose StatementEnd
