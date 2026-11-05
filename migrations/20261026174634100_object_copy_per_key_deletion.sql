-- migration-compat: expand
-- filename: 20261026174634100_object_copy_per_key_deletion.sql
--
-- ADR-20261025115958 amendment: a cross-bucket copy is fenced only by an
-- unsettled deletion of its source key. The destination is an ordinary write:
-- write admission fences it per key, and deletion admission refuses a key with
-- a pending write or a live multipart upload. Only the deletion clause of
-- assert_object_copy_source_authority changes.

-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
CREATE OR REPLACE FUNCTION assert_object_copy_source_authority(a uuid, destination uuid, subject text, source uuid, key text, epoch uuid) RETURNS void
    LANGUAGE plpgsql
    AS $_$
DECLARE c object_storage_s3_credentials; p object_storage_s3_credentials; g object_s3_copy_source_grants;
BEGIN
 IF subject !~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_copy_source_fenced',MESSAGE='Invalid copy subject';
 END IF;
 SELECT * INTO c FROM object_storage_s3_credentials WHERE id=subject::uuid FOR SHARE;
 IF c.id IS NULL OR c.account_id<>a OR c.bucket_id<>destination OR c.status<>'active' OR
  c.permission NOT IN ('write','read_write') OR c.url_request IS NOT NULL THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_copy_source_fenced',MESSAGE='Copy writer is unavailable';
 END IF;
 SELECT * INTO p FROM object_storage_s3_credentials WHERE id=coalesce(c.rotation_parent_id,c.id) FOR SHARE;
 SELECT * INTO g FROM object_s3_copy_source_grants WHERE credential_id=p.id AND source_bucket_id=source FOR SHARE;
 IF p.id IS NULL OR p.account_id<>a OR p.bucket_id<>destination OR p.status<>'active' OR
  p.permission NOT IN ('write','read_write') OR p.url_request IS NOT NULL OR g.id IS NULL OR
  g.id<>epoch OR g.account_id<>a OR g.bucket_id<>destination OR key='' OR octet_length(key)>1024 OR
  left(key,length(g.prefix))<>g.prefix OR key ~ E'[\\r\\n]' OR
  NOT EXISTS(SELECT 1 FROM object_buckets d JOIN object_buckets s ON s.id=source
    WHERE d.id=destination AND d.account_id=a AND s.account_id=a AND d.state='ready' AND s.state='ready'
    AND (d.backend_id,d.backend_fingerprint)=(s.backend_id,s.backend_fingerprint)) OR
  EXISTS(SELECT 1 FROM object_deletions WHERE bucket_id=source AND object_key=key AND state IN ('prepared','dispatched')) THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_copy_source_fenced',MESSAGE='Copy source authority changed';
 END IF;
END $_$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
CREATE OR REPLACE FUNCTION assert_object_copy_source_authority(a uuid, destination uuid, subject text, source uuid, key text, epoch uuid) RETURNS void
    LANGUAGE plpgsql
    AS $_$
DECLARE c object_storage_s3_credentials; p object_storage_s3_credentials; g object_s3_copy_source_grants;
BEGIN
 IF subject !~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_copy_source_fenced',MESSAGE='Invalid copy subject';
 END IF;
 SELECT * INTO c FROM object_storage_s3_credentials WHERE id=subject::uuid FOR SHARE;
 IF c.id IS NULL OR c.account_id<>a OR c.bucket_id<>destination OR c.status<>'active' OR
  c.permission NOT IN ('write','read_write') OR c.url_request IS NOT NULL THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_copy_source_fenced',MESSAGE='Copy writer is unavailable';
 END IF;
 SELECT * INTO p FROM object_storage_s3_credentials WHERE id=coalesce(c.rotation_parent_id,c.id) FOR SHARE;
 SELECT * INTO g FROM object_s3_copy_source_grants WHERE credential_id=p.id AND source_bucket_id=source FOR SHARE;
 IF p.id IS NULL OR p.account_id<>a OR p.bucket_id<>destination OR p.status<>'active' OR
  p.permission NOT IN ('write','read_write') OR p.url_request IS NOT NULL OR g.id IS NULL OR
  g.id<>epoch OR g.account_id<>a OR g.bucket_id<>destination OR key='' OR octet_length(key)>1024 OR
  left(key,length(g.prefix))<>g.prefix OR key ~ E'[\\r\\n]' OR
  NOT EXISTS(SELECT 1 FROM object_buckets d JOIN object_buckets s ON s.id=source
    WHERE d.id=destination AND d.account_id=a AND s.account_id=a AND d.state='ready' AND s.state='ready'
    AND (d.backend_id,d.backend_fingerprint)=(s.backend_id,s.backend_fingerprint)) OR
  EXISTS(SELECT 1 FROM object_deletions WHERE bucket_id IN (destination,source) AND state IN ('prepared','dispatched')) THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_copy_source_fenced',MESSAGE='Copy source authority changed';
 END IF;
END $_$;
-- +goose StatementEnd
