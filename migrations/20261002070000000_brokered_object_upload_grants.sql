-- +goose Up
-- ADR-375: upload URLs authorize requests through Gregale, where source
-- fences and durable active-writer receipts cover the actual provider IO.
CREATE TABLE object_storage_upload_grants (
    id uuid PRIMARY KEY,
    bucket_id uuid NOT NULL REFERENCES object_buckets(id) ON DELETE CASCADE,
	account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
	app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    token_hash text NOT NULL UNIQUE CHECK (token_hash ~ '^[0-9a-f]{64}$'),
    kind text NOT NULL CHECK (kind IN ('put','multipart_part')),
    object_key text NOT NULL CHECK (octet_length(object_key) BETWEEN 1 AND 1024),
    size_bytes bigint NOT NULL CHECK (size_bytes BETWEEN 0 AND 5368709120),
    headers jsonb NOT NULL CHECK (jsonb_typeof(headers) = 'object'),
    upload_id uuid REFERENCES object_storage_multipart_uploads(id) ON DELETE CASCADE,
    provider_upload_id text,
    part_number integer NOT NULL DEFAULT 0,
    backend_id text NOT NULL CHECK (backend_id <> ''),
    backend_fingerprint text NOT NULL CHECK (backend_fingerprint <> ''),
    physical_name text NOT NULL CHECK (physical_name <> ''),
    created_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    CHECK (expires_at > created_at AND expires_at <= created_at + interval '900 seconds'),
    CHECK ((kind='put' AND upload_id IS NULL AND provider_upload_id IS NULL AND part_number=0)
       OR (kind='multipart_part' AND upload_id IS NOT NULL AND provider_upload_id IS NOT NULL AND provider_upload_id <> '' AND part_number BETWEEN 1 AND 10000 AND size_bytes > 0))
);
CREATE INDEX object_storage_upload_grants_expiry_idx ON object_storage_upload_grants(expires_at,id);

-- +goose Down
DROP TABLE object_storage_upload_grants;
