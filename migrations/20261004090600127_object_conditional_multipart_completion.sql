-- +goose Up
ALTER TABLE object_storage_multipart_uploads
    ADD COLUMN completion_if_match text NOT NULL DEFAULT '',
    ADD COLUMN completion_if_none_match text NOT NULL DEFAULT '',
    ADD COLUMN completion_error_code text NOT NULL DEFAULT '',
    DROP CONSTRAINT object_storage_multipart_uploads_state_check,
    ADD CONSTRAINT object_storage_multipart_uploads_state_check CHECK (state IN ('initiating','active','completing','completing_conditional','aborting','completed','aborted')),
    ADD CONSTRAINT object_multipart_completion_conditions CHECK (
        octet_length(completion_if_match)<=256 AND completion_if_match !~ '[[:cntrl:]]'
        AND completion_if_none_match IN ('','*')
        AND (completion_if_match='' OR completion_if_none_match='')
        AND (state NOT IN ('initiating','active','completing') OR (completion_if_match='' AND completion_if_none_match=''))
        AND (state<>'completing_conditional' OR (part_count=0 AND (completion_if_match<>'' OR completion_if_none_match<>'')))
    ),
    ADD CONSTRAINT object_multipart_completion_error CHECK (
        completion_error_code IN ('','precondition_failed','conditional_conflict','conditional_not_found')
        AND (completion_error_code='' OR (state IN ('aborting','aborted') AND (completion_if_match<>'' OR completion_if_none_match<>'')))
    );
DROP INDEX object_storage_multipart_live_key_idx;
CREATE UNIQUE INDEX object_storage_multipart_live_key_idx ON object_storage_multipart_uploads (bucket_id,object_key)
    WHERE state IN ('initiating','active','completing','completing_conditional','aborting');
DROP INDEX object_storage_multipart_retry_idx;
CREATE INDEX object_storage_multipart_retry_idx ON object_storage_multipart_uploads (retry_at,id)
    WHERE state IN ('initiating','completing','completing_conditional','aborting');
-- +goose StatementBegin
CREATE FUNCTION fence_object_multipart_completion_conditions() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.state<>'active' AND (NEW.completion_if_match IS DISTINCT FROM OLD.completion_if_match
        OR NEW.completion_if_none_match IS DISTINCT FROM OLD.completion_if_none_match) THEN
        RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Multipart completion conditions are immutable';
    END IF;
    IF OLD.completion_error_code<>'' AND NEW.completion_error_code IS DISTINCT FROM OLD.completion_error_code THEN
        RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Multipart completion failure is immutable';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER object_multipart_completion_conditions_immutable BEFORE UPDATE ON object_storage_multipart_uploads
    FOR EACH ROW EXECUTE FUNCTION fence_object_multipart_completion_conditions();

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM object_storage_multipart_uploads WHERE
        (completion_if_match<>'' OR completion_if_none_match<>'') AND state NOT IN ('completed','aborted')) THEN
        RAISE EXCEPTION 'Settle conditional multipart completions and cleanup before rollback';
    END IF;
END $$;
-- +goose StatementEnd
DROP TRIGGER object_multipart_completion_conditions_immutable ON object_storage_multipart_uploads;
DROP FUNCTION fence_object_multipart_completion_conditions();
DROP INDEX object_storage_multipart_live_key_idx;
CREATE UNIQUE INDEX object_storage_multipart_live_key_idx ON object_storage_multipart_uploads (bucket_id,object_key)
    WHERE state IN ('initiating','active','completing','aborting');
DROP INDEX object_storage_multipart_retry_idx;
CREATE INDEX object_storage_multipart_retry_idx ON object_storage_multipart_uploads (retry_at,id)
    WHERE state IN ('initiating','completing','aborting');
ALTER TABLE object_storage_multipart_uploads
    DROP CONSTRAINT object_multipart_completion_error,
    DROP CONSTRAINT object_multipart_completion_conditions,
    DROP CONSTRAINT object_storage_multipart_uploads_state_check,
    ADD CONSTRAINT object_storage_multipart_uploads_state_check CHECK (state IN ('initiating','active','completing','aborting','completed','aborted')),
    DROP COLUMN completion_error_code,DROP COLUMN completion_if_none_match,DROP COLUMN completion_if_match;
