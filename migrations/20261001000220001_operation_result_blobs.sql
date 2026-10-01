-- +goose Up
-- +goose StatementBegin
-- ADR-385. A write intent precedes external storage I/O. No cascading FKs:
-- account/app/result deletion must leave the cleanup receipt discoverable.
CREATE TABLE IF NOT EXISTS customer_operation_result_blobs (
    id uuid PRIMARY KEY,
    operation_id uuid NOT NULL,
    account_id uuid NOT NULL,
    generation integer NOT NULL CHECK (generation > 0),
    execution_id uuid NOT NULL,
    attempt integer NOT NULL CHECK (attempt > 0),
    report_id text NOT NULL CHECK (octet_length(report_id) BETWEEN 1 AND 128),
    fingerprint text NOT NULL CHECK (fingerprint ~ '^[0-9a-f]{64}$'),
    storage_key text NOT NULL UNIQUE,
    size_bytes bigint NOT NULL CHECK (size_bytes >= 0),
    state text NOT NULL CHECK (state IN ('staging','retained','deleting')),
    expires_at timestamptz NOT NULL,
    next_attempt_at timestamptz NOT NULL,
    lease_token text NOT NULL DEFAULT '',
    lease_until timestamptz,
    CHECK (storage_key = 'operation-results/' || account_id::text || '/' || operation_id::text || '/' || id::text)
);
CREATE INDEX IF NOT EXISTS customer_operation_result_blobs_account_idx ON customer_operation_result_blobs(account_id);
CREATE INDEX IF NOT EXISTS customer_operation_result_blobs_operation_idx ON customer_operation_result_blobs(operation_id);
CREATE INDEX IF NOT EXISTS customer_operation_result_blobs_cleanup_idx ON customer_operation_result_blobs(next_attempt_at,expires_at);
-- +goose StatementEnd

-- +goose Down
-- Forward-only: deleting the ledger could leak retained customer bytes.
SELECT 1;
