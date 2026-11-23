-- filename: 20261123184806100_object_storage_workload_credentials.sql
-- migration-compat: expand
-- ADR-20261122191423 slice 2: short-lived S3 credentials a workload fetches
-- for a bucket its gregale.yaml `uses:` grants, through the guest's loopback
-- credential endpoint. They live apart from object_storage_s3_credentials so
-- environment clones, credential inventories and the per-bucket key cap never
-- see them. Each row is one instance's credential for one bucket; it expires
-- within the hour and apid sweeps expired rows. Access keys start GRGW (the
-- customer and compute keys start GRGA), so the gateway resolves a key in one
-- table. The table is new, so the previous release neither reads nor writes
-- it. Re-runnable.

-- +goose Up
SET LOCAL lock_timeout = '5s';

CREATE TABLE IF NOT EXISTS object_storage_workload_credentials (
    id uuid PRIMARY KEY,
    account_id uuid NOT NULL,
    bucket_id uuid NOT NULL,
    caller_app_id uuid NOT NULL,
    instance_id text NOT NULL CHECK (octet_length(instance_id) BETWEEN 1 AND 128),
    access_key_id text NOT NULL UNIQUE CHECK (access_key_id ~ '^GRGW[A-Z2-7]{16}$'),
    secret_sealed bytea NOT NULL CHECK (octet_length(secret_sealed) > 0),
    kid text NOT NULL CHECK (length(kid) BETWEEN 1 AND 255),
    permission text NOT NULL CHECK (permission IN ('read', 'write', 'read_write')),
    key_prefix text CHECK (key_prefix IS NULL OR (octet_length(key_prefix) BETWEEN 1 AND 512 AND left(key_prefix, 1) <> '/')),
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    CHECK (expires_at > created_at AND expires_at <= created_at + interval '65 minutes')
);
COMMENT ON TABLE object_storage_workload_credentials IS 'Short-lived per-instance S3 credentials for buckets granted by gregale.yaml uses:; written by apid, resolved by s3-gatewayd, swept by apid after expiry (ADR-20261122191423).';

CREATE INDEX IF NOT EXISTS object_storage_workload_credentials_expires_idx
    ON object_storage_workload_credentials (expires_at);
-- apid counts one instance's live credentials per bucket on every issue, and
-- revokes an app's credentials when its uses: drops a grant.
CREATE INDEX IF NOT EXISTS object_storage_workload_credentials_instance_idx
    ON object_storage_workload_credentials (instance_id, bucket_id);
CREATE INDEX IF NOT EXISTS object_storage_workload_credentials_caller_idx
    ON object_storage_workload_credentials (caller_app_id);

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TABLE IF EXISTS object_storage_workload_credentials;
