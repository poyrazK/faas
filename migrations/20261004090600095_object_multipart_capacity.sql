-- +goose Up
-- +goose StatementBegin
DO $$ BEGIN
    IF to_regclass('object_storage_multipart_part_grants') IS NULL
     AND EXISTS (SELECT 1 FROM object_storage_multipart_uploads
        WHERE part_count=0 AND state IN ('initiating','active','completing','aborting')) THEN
        RAISE EXCEPTION 'Drain active branded S3 multipart uploads before enabling per-part capacity accounting';
    END IF;
END $$;
-- +goose StatementEnd
CREATE TABLE IF NOT EXISTS object_storage_multipart_part_grants (
    upload_id uuid NOT NULL REFERENCES object_storage_multipart_uploads(id) ON DELETE CASCADE,
    part_number integer NOT NULL CHECK (part_number BETWEEN 1 AND 10000),
    max_bytes bigint NOT NULL CHECK (max_bytes > 0 AND max_bytes <= 5368709120),
    PRIMARY KEY (upload_id, part_number)
);

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM object_storage_multipart_part_grants g
        JOIN object_storage_multipart_uploads u ON u.id=g.upload_id
        WHERE u.state <> 'completed') THEN
        RAISE EXCEPTION 'Settle multipart capacity reservations before rollback';
    END IF;
END $$;
-- +goose StatementEnd
DROP TABLE object_storage_multipart_part_grants;
