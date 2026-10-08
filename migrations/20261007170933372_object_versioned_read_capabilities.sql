-- +goose Up
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION valid_object_versioned_url_request(r jsonb) RETURNS boolean LANGUAGE plpgsql IMMUTABLE STRICT AS $$
BEGIN
 IF r ? 'version_id' THEN
  IF r->>'method' NOT IN ('GET','HEAD') OR jsonb_typeof(r->'version_id') IS DISTINCT FROM 'string' OR
   r->>'version_id' !~ '^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$' THEN RETURN false; END IF;
 END IF;
 RETURN coalesce(valid_object_event_protected_url_request(r-'version_id'),false);
EXCEPTION WHEN OTHERS THEN RETURN false;
END $$;
ALTER TABLE object_storage_s3_credentials DROP CONSTRAINT IF EXISTS object_s3_event_protected_url_request;
ALTER TABLE object_storage_s3_credentials DROP CONSTRAINT IF EXISTS object_s3_versioned_url_request;
ALTER TABLE object_storage_s3_credentials ADD CONSTRAINT object_s3_versioned_url_request CHECK(url_request IS NULL OR valid_object_versioned_url_request(url_request));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM object_storage_s3_credentials WHERE url_request ? 'version_id') THEN
  RAISE EXCEPTION 'Cannot discard persisted version-bound read authority';
 END IF;
END $$;
ALTER TABLE object_storage_s3_credentials DROP CONSTRAINT IF EXISTS object_s3_versioned_url_request;
ALTER TABLE object_storage_s3_credentials ADD CONSTRAINT object_s3_event_protected_url_request CHECK(url_request IS NULL OR valid_object_event_protected_url_request(url_request));
DROP FUNCTION IF EXISTS valid_object_versioned_url_request(jsonb);
-- +goose StatementEnd
