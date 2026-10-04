-- +goose Up
-- ADR-531: a source bucket cannot be drained by expiring a request lease.
-- Successful synchronous mutations remove their receipts. Native write grants
-- remain outstanding until a future provider-specific revocation proof exists.
CREATE TABLE object_bucket_mutations (
    id uuid PRIMARY KEY,
    bucket_id uuid NOT NULL REFERENCES object_buckets(id) ON DELETE CASCADE,
    kind text NOT NULL CHECK (kind IN ('request', 'native_grant')),
    backend_id text NOT NULL CHECK (backend_id <> ''),
    backend_fingerprint text NOT NULL CHECK (backend_fingerprint <> ''),
    physical_name text NOT NULL CHECK (physical_name <> ''),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX object_bucket_mutations_bucket_idx ON object_bucket_mutations(bucket_id);

-- No expiry: losing the coordinator must not silently reopen the source.
-- These private primitives alone do not authorize a full clone checkpoint.
CREATE TABLE object_bucket_write_fences (
    bucket_id uuid PRIMARY KEY REFERENCES object_buckets(id) ON DELETE RESTRICT,
    token uuid NOT NULL,
    backend_id text NOT NULL CHECK (backend_id <> ''),
    backend_fingerprint text NOT NULL CHECK (backend_fingerprint <> ''),
    physical_name text NOT NULL CHECK (physical_name <> ''),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

-- +goose Down
DROP TABLE object_bucket_write_fences;
DROP TABLE object_bucket_mutations;
