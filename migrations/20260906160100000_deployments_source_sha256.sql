-- +goose Up
-- +goose StatementBegin

-- Persist the digest of the exact source archive handed to builderd. The
-- column is nullable for pre-existing deployments; new source enqueue paths
-- populate it before publishing durable build work.
ALTER TABLE deployments
    ADD COLUMN IF NOT EXISTS source_sha256 text;

ALTER TABLE deployments
    DROP CONSTRAINT IF EXISTS deployments_source_sha256_shape_chk;

ALTER TABLE deployments
    ADD CONSTRAINT deployments_source_sha256_shape_chk
    CHECK (source_sha256 IS NULL OR source_sha256 ~ '^[0-9a-f]{64}$');

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE deployments DROP CONSTRAINT IF EXISTS deployments_source_sha256_shape_chk;
ALTER TABLE deployments DROP COLUMN IF EXISTS source_sha256;

-- +goose StatementEnd
