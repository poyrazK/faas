-- migration-compat: expand
-- filename: 20261026051220122_object_bucket_cors.sql
--
-- ADR-20261025202805: a customer's CORS rules for one bucket, written by apid
-- and read by s3-gatewayd. A new table; the previous release never reads it.

-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
CREATE TABLE IF NOT EXISTS object_bucket_cors (
    bucket_id uuid PRIMARY KEY REFERENCES object_buckets(id) ON DELETE CASCADE,
    rules jsonb NOT NULL CHECK (jsonb_typeof(rules) = 'array' AND jsonb_array_length(rules) BETWEEN 1 AND 100 AND octet_length(rules::text) <= 65536),
    revision bigint NOT NULL CHECK (revision > 0),
    updated_at timestamptz NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
DROP TABLE IF EXISTS object_bucket_cors;
-- +goose StatementEnd
