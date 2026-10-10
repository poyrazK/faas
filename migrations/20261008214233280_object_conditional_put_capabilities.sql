-- +goose Up
-- +goose StatementBegin
-- Keep this validator independent of older migration definitions. Unknown
-- fields, version-bound reads and protected writes retain their own checks.
CREATE OR REPLACE FUNCTION valid_object_conditional_url_request(r jsonb) RETURNS boolean LANGUAGE plpgsql IMMUTABLE STRICT AS $$
BEGIN
 IF NOT coalesce(valid_object_versioned_url_request(r-ARRAY['if_match','if_none_match']),false) OR octet_length(r::text)>32768 THEN RETURN false; END IF;
 IF NOT (r ? 'if_match' OR r ? 'if_none_match') THEN RETURN true; END IF;
 IF r->>'method'<>'PUT' OR r ? 'multipart' OR r ? 'if_match' AND r ? 'if_none_match' THEN RETURN false; END IF;
 IF r ? 'if_match' AND (jsonb_typeof(r->'if_match') IS DISTINCT FROM 'string' OR octet_length(r->>'if_match') NOT BETWEEN 1 AND 256 OR (r->>'if_match') ~ '[\x01-\x1f\x7f]') THEN RETURN false; END IF;
 IF r ? 'if_none_match' AND (jsonb_typeof(r->'if_none_match') IS DISTINCT FROM 'string' OR r->>'if_none_match'<>'*') THEN RETURN false; END IF;
 RETURN true;
EXCEPTION WHEN OTHERS THEN RETURN false;
END $$;
ALTER TABLE object_storage_s3_credentials DROP CONSTRAINT IF EXISTS object_s3_versioned_url_request;
ALTER TABLE object_storage_s3_credentials DROP CONSTRAINT IF EXISTS object_s3_conditional_url_request;
ALTER TABLE object_storage_s3_credentials ADD CONSTRAINT object_s3_conditional_url_request CHECK(url_request IS NULL OR valid_object_conditional_url_request(url_request));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM object_storage_s3_credentials WHERE url_request ? 'if_match' OR url_request ? 'if_none_match') THEN
  RAISE EXCEPTION 'Cannot discard persisted conditional write authority';
 END IF;
END $$;
ALTER TABLE object_storage_s3_credentials DROP CONSTRAINT IF EXISTS object_s3_conditional_url_request;
ALTER TABLE object_storage_s3_credentials ADD CONSTRAINT object_s3_versioned_url_request CHECK(url_request IS NULL OR valid_object_versioned_url_request(url_request));
DROP FUNCTION IF EXISTS valid_object_conditional_url_request(jsonb);
-- +goose StatementEnd
