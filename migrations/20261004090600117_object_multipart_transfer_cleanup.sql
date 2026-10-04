-- +goose Up
-- Pause branded multipart writes and drain live sessions before upgrading.
-- Legacy grants remain conservative: their transfers were never tracked.
-- +goose StatementBegin
DO $$ BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_attribute WHERE attrelid='object_storage_multipart_uploads'::regclass AND attname='part_revision' AND NOT attisdropped)
     AND EXISTS (SELECT 1 FROM object_storage_multipart_uploads
        WHERE part_count=0 AND state IN ('initiating','active','completing','aborting')) THEN
        RAISE EXCEPTION 'Drain active branded S3 multipart uploads before enabling transfer fencing';
    END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE object_storage_multipart_uploads ADD COLUMN IF NOT EXISTS part_revision bigint NOT NULL DEFAULT 0 CHECK (part_revision >= 0);
ALTER TABLE object_storage_multipart_part_grants ADD COLUMN IF NOT EXISTS cleanup_tracked boolean NOT NULL DEFAULT false;
ALTER TABLE object_storage_multipart_part_grants ADD COLUMN IF NOT EXISTS transfer_token text;
ALTER TABLE object_storage_multipart_part_grants ADD COLUMN IF NOT EXISTS unsafe_until timestamptz;
-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='object_storage_multipart_part_grants'::regclass AND conname='object_multipart_transfer_pair') THEN
  ALTER TABLE object_storage_multipart_part_grants ADD CONSTRAINT object_multipart_transfer_pair CHECK (
        (transfer_token IS NULL AND unsafe_until IS NULL) OR
        (transfer_token IS NOT NULL AND length(transfer_token) BETWEEN 1 AND 128 AND unsafe_until IS NOT NULL AND isfinite(unsafe_until)));
 END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM object_storage_multipart_part_grants WHERE transfer_token IS NOT NULL) THEN
        RAISE EXCEPTION 'Settle multipart transfers before rollback';
    END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE object_storage_multipart_part_grants DROP CONSTRAINT object_multipart_transfer_pair,
    DROP COLUMN unsafe_until, DROP COLUMN transfer_token, DROP COLUMN cleanup_tracked;
ALTER TABLE object_storage_multipart_uploads DROP COLUMN part_revision;
