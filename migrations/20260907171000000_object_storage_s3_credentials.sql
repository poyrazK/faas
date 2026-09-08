-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS object_storage_s3_credentials (
    id uuid PRIMARY KEY,
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    bucket_id uuid NOT NULL REFERENCES object_buckets(id) ON DELETE CASCADE,
    access_key_id text NOT NULL UNIQUE CHECK (access_key_id ~ '^GRGA[A-Z2-7]{16}$'),
    secret_sealed bytea NOT NULL CHECK (length(secret_sealed) > 0),
    kid text NOT NULL CHECK (length(kid) BETWEEN 1 AND 255),
    label text NOT NULL CHECK (length(label) BETWEEN 1 AND 64),
    permission text NOT NULL CHECK (permission IN ('read','write','read_write')),
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('active','revoked')),
    created_at timestamptz NOT NULL DEFAULT now(),
    last_used_at timestamptz,
    revoked_at timestamptz,
    CHECK ((status = 'active' AND revoked_at IS NULL) OR (status = 'revoked' AND revoked_at IS NOT NULL))
);
CREATE INDEX IF NOT EXISTS object_storage_s3_credentials_bucket_active_idx
    ON object_storage_s3_credentials (account_id, bucket_id, created_at, id)
    WHERE status = 'active';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS object_storage_s3_credentials;
-- +goose StatementEnd
