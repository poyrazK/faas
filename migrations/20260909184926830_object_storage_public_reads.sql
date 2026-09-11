-- filename: 20260909184926830_object_storage_public_reads.sql

-- +goose Up
-- Public-read buckets are mounted at a stable path on the app hostname.
-- Keep the switch and path on the durable bucket placement so gatewayd-public
-- can serve assets without consulting or waking the app runtime.
ALTER TABLE object_buckets
    ADD COLUMN IF NOT EXISTS public_read boolean NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS serve_at text;

-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'object_buckets_serve_at_check'
          AND conrelid = 'object_buckets'::regclass
    ) THEN
        ALTER TABLE object_buckets
            ADD CONSTRAINT object_buckets_serve_at_check CHECK (
                (NOT public_read AND serve_at IS NULL) OR (public_read AND (
                    serve_at ~ '^/[A-Za-z0-9][A-Za-z0-9._~/-]*$'
                    AND serve_at !~ '//'
                    AND serve_at !~ '/\.$'
                    AND serve_at !~ '(^|/)\.\.($|/)'
                    AND right(serve_at, 1) <> '/'
                ))
            );
    END IF;
END
$$;
-- +goose StatementEnd

CREATE UNIQUE INDEX IF NOT EXISTS object_buckets_public_serve_at_idx
    ON object_buckets (app_id, serve_at)
    WHERE public_read AND state <> 'deleted' AND serve_at IS NOT NULL;

ALTER TABLE object_storage_request_metrics
    ADD COLUMN IF NOT EXISTS egress_bytes bigint NOT NULL DEFAULT 0
        CHECK (egress_bytes BETWEEN 0 AND 1152921504606846976);

-- +goose Down
DROP INDEX IF EXISTS object_buckets_public_serve_at_idx;
ALTER TABLE object_buckets DROP CONSTRAINT IF EXISTS object_buckets_serve_at_check;
ALTER TABLE object_buckets DROP COLUMN IF EXISTS serve_at, DROP COLUMN IF EXISTS public_read;
ALTER TABLE object_storage_request_metrics DROP COLUMN IF EXISTS egress_bytes;
