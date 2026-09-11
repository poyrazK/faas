-- +goose Up
-- +goose StatementBegin
-- Standard S3 CreateMultipartUpload does not carry the final object size.
-- Zero-valued size/part layout identifies those public S3 sessions; the
-- existing JSON control-plane multipart API continues to require a fixed
-- layout at initiation. Completed public sessions retain zero part-layout
-- fields because their provider parts are variable-sized.
ALTER TABLE object_storage_multipart_uploads DROP CONSTRAINT IF EXISTS object_storage_multipart_uploads_size_bytes_check;
ALTER TABLE object_storage_multipart_uploads ADD CONSTRAINT object_storage_multipart_uploads_size_bytes_check
    CHECK (size_bytes BETWEEN 0 AND 5497558138880);
ALTER TABLE object_storage_multipart_uploads DROP CONSTRAINT IF EXISTS object_storage_multipart_uploads_part_size_bytes_check;
ALTER TABLE object_storage_multipart_uploads ADD CONSTRAINT object_storage_multipart_uploads_part_size_bytes_check
    CHECK (part_size_bytes BETWEEN 0 AND 5368709120);
ALTER TABLE object_storage_multipart_uploads DROP CONSTRAINT IF EXISTS object_storage_multipart_uploads_part_count_check;
ALTER TABLE object_storage_multipart_uploads ADD CONSTRAINT object_storage_multipart_uploads_part_count_check
    CHECK (part_count BETWEEN 0 AND 10000);
ALTER TABLE object_storage_multipart_uploads DROP CONSTRAINT IF EXISTS object_storage_multipart_uploads_layout_check;
ALTER TABLE object_storage_multipart_uploads ADD CONSTRAINT object_storage_multipart_uploads_layout_check
    CHECK ((size_bytes = 0 AND part_size_bytes = 0 AND part_count = 0)
        OR (size_bytes > 0 AND part_size_bytes = 0 AND part_count = 0)
        OR (size_bytes > 0 AND part_size_bytes > 0 AND part_count > 0));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM object_storage_multipart_uploads
        WHERE size_bytes > 0 AND part_size_bytes = 0 AND part_count = 0
    ) THEN
        RAISE EXCEPTION 'cannot roll back public S3 multipart layout while completed sessions remain';
    END IF;
END $$;
ALTER TABLE object_storage_multipart_uploads DROP CONSTRAINT IF EXISTS object_storage_multipart_uploads_layout_check;
ALTER TABLE object_storage_multipart_uploads DROP CONSTRAINT IF EXISTS object_storage_multipart_uploads_size_bytes_check;
ALTER TABLE object_storage_multipart_uploads ADD CONSTRAINT object_storage_multipart_uploads_size_bytes_check
    CHECK (size_bytes BETWEEN 1 AND 5497558138880);
ALTER TABLE object_storage_multipart_uploads DROP CONSTRAINT IF EXISTS object_storage_multipart_uploads_part_size_bytes_check;
ALTER TABLE object_storage_multipart_uploads ADD CONSTRAINT object_storage_multipart_uploads_part_size_bytes_check
    CHECK (part_size_bytes BETWEEN 1 AND 5368709120);
ALTER TABLE object_storage_multipart_uploads DROP CONSTRAINT IF EXISTS object_storage_multipart_uploads_part_count_check;
ALTER TABLE object_storage_multipart_uploads ADD CONSTRAINT object_storage_multipart_uploads_part_count_check
    CHECK (part_count BETWEEN 1 AND 10000);
-- +goose StatementEnd
