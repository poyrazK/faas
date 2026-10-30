-- migration-compat: expand
-- filename: 20261030202104676_object_storage_inventory_scans.sql
--
-- ADR-20261030100835: a bucket's inventory listing in progress, so a bucket
-- too large to list in one attempt resumes from its last listed key. A new
-- table; the previous release never reads it.

-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
CREATE TABLE IF NOT EXISTS object_storage_inventory_scans (
    bucket_id uuid PRIMARY KEY REFERENCES object_buckets(id) ON DELETE CASCADE,
    started_at timestamptz NOT NULL,
    progress_at timestamptz NOT NULL,
    after_key text NOT NULL DEFAULT '' CHECK (octet_length(after_key) <= 1024),
    listed_bytes bigint NOT NULL DEFAULT 0 CHECK (listed_bytes >= 0),
    listed_keys bigint NOT NULL DEFAULT 0 CHECK (listed_keys >= 0),
    pages integer NOT NULL DEFAULT 0 CHECK (pages >= 0),
    complete boolean NOT NULL DEFAULT false,
    CHECK (progress_at >= started_at),
    CHECK (pages > 0 OR (after_key = '' AND listed_bytes = 0 AND listed_keys = 0 AND NOT complete))
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
DROP TABLE IF EXISTS object_storage_inventory_scans;
-- +goose StatementEnd
