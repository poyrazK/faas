-- +goose Up
ALTER TABLE object_buckets
    ADD COLUMN attempt_count integer NOT NULL DEFAULT 0 CHECK (attempt_count BETWEEN 0 AND 30),
    ADD COLUMN retry_at timestamptz NOT NULL DEFAULT now(),
    ADD COLUMN last_error_code text NOT NULL DEFAULT '' CHECK (last_error_code IN ('', 'temporary', 'configuration', 'conflict', 'invalid'));
CREATE INDEX object_buckets_recovery_idx ON object_buckets (retry_at, id)
    WHERE state IN ('provisioning', 'deleting');

-- +goose Down
DROP INDEX object_buckets_recovery_idx;
ALTER TABLE object_buckets DROP COLUMN last_error_code, DROP COLUMN retry_at, DROP COLUMN attempt_count;
