-- filename: 20261004090600537_object_s3_copy_source_grants.sql

-- +goose Up
-- +goose StatementBegin
-- Retain published identities after revocation so old prepared work cannot be
-- revived by an older writer that reuses an identity. Account deletion owns
-- cleanup; ordinary credential and bucket deletion must preserve this history.
CREATE TABLE object_s3_copy_source_epochs (
 id uuid PRIMARY KEY CHECK(id<>'00000000-0000-0000-0000-000000000000'),
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX object_s3_copy_source_epochs_account ON object_s3_copy_source_epochs(account_id);
CREATE FUNCTION protect_object_s3_copy_source_epoch() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='UPDATE' OR EXISTS(SELECT 1 FROM accounts WHERE id=OLD.account_id) THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_copy_source_fenced',MESSAGE='Published copy source identities are immutable';
 END IF;
 RETURN OLD;
END $$;
CREATE TRIGGER object_copy_source_epoch_bound BEFORE UPDATE OR DELETE ON object_s3_copy_source_epochs
 FOR EACH ROW EXECUTE FUNCTION protect_object_s3_copy_source_epoch();
CREATE TABLE object_s3_copy_source_grants (
 id uuid NOT NULL UNIQUE CHECK(id<>'00000000-0000-0000-0000-000000000000'),
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 credential_id uuid NOT NULL REFERENCES object_storage_s3_credentials(id) ON DELETE CASCADE,
 bucket_id uuid NOT NULL REFERENCES object_buckets(id) ON DELETE CASCADE,
 source_bucket_id uuid NOT NULL REFERENCES object_buckets(id) ON DELETE CASCADE CHECK(source_bucket_id<>bucket_id),
 prefix text NOT NULL DEFAULT '' CHECK(octet_length(prefix)<=1024 AND prefix !~ E'[\\r\\n]'),
 created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(credential_id,source_bucket_id)
);
CREATE FUNCTION protect_object_s3_copy_source_grant() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE c object_storage_s3_credentials; d object_buckets; s object_buckets;
BEGIN
 SELECT * INTO c FROM object_storage_s3_credentials WHERE id=NEW.credential_id FOR NO KEY UPDATE;
 IF NOT FOUND OR c.account_id<>NEW.account_id OR c.bucket_id<>NEW.bucket_id OR
 c.status<>'active' OR c.permission NOT IN ('write','read_write') OR c.url_request IS NOT NULL OR c.rotation_parent_id IS NOT NULL THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_copy_source_fenced',MESSAGE='Copy source grants require an owned destination writer';
 END IF;
 SELECT * INTO d FROM object_buckets WHERE id=NEW.bucket_id;
 SELECT * INTO s FROM object_buckets WHERE id=NEW.source_bucket_id;
 IF d.id IS NULL OR s.id IS NULL OR d.account_id<>NEW.account_id OR s.account_id<>NEW.account_id OR
 d.state<>'ready' OR s.state<>'ready' OR (d.backend_id,d.backend_fingerprint) IS DISTINCT FROM (s.backend_id,s.backend_fingerprint) OR
 EXISTS(SELECT 1 FROM object_deletions WHERE bucket_id IN (d.id,s.id) AND state IN ('prepared','dispatched')) THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_copy_source_fenced',MESSAGE='Copy sources require owned ready buckets on one placement';
 END IF;
 IF TG_OP='UPDATE' THEN
  IF (NEW.account_id,NEW.credential_id,NEW.bucket_id,NEW.source_bucket_id,NEW.created_at) IS DISTINCT FROM
   (OLD.account_id,OLD.credential_id,OLD.bucket_id,OLD.source_bucket_id,OLD.created_at) OR
   (NEW.prefix IS DISTINCT FROM OLD.prefix AND NEW.id=OLD.id) THEN
   RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_copy_source_fenced',MESSAGE='Copy source ownership and grant epochs are immutable';
  END IF;
 ELSIF NOT EXISTS(SELECT 1 FROM object_s3_copy_source_grants WHERE credential_id=c.id AND source_bucket_id=s.id) AND
  (SELECT count(*) FROM object_s3_copy_source_grants WHERE credential_id=c.id)>=32 THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_copy_source_fenced',MESSAGE='Copy source grant limit exceeded';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER object_copy_source_grant_bound BEFORE INSERT OR UPDATE ON object_s3_copy_source_grants
 FOR EACH ROW EXECUTE FUNCTION protect_object_s3_copy_source_grant();
CREATE FUNCTION record_object_s3_copy_source_epoch() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 -- AFTER runs only for the winning insert/update of an upsert. An idempotent
 -- update of the current identity is valid; every new identity is single use.
 IF TG_OP='INSERT' OR NEW.id IS DISTINCT FROM OLD.id THEN
  INSERT INTO object_s3_copy_source_epochs(id,account_id) VALUES(NEW.id,NEW.account_id) ON CONFLICT DO NOTHING;
  IF NOT FOUND THEN
   RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_copy_source_fenced',MESSAGE='Copy source identity was already published';
  END IF;
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER object_copy_source_epoch_recorded AFTER INSERT OR UPDATE ON object_s3_copy_source_grants
 FOR EACH ROW EXECUTE FUNCTION record_object_s3_copy_source_epoch();

CREATE FUNCTION assert_object_copy_source_authority(a uuid,destination uuid,subject text,source uuid,key text,epoch uuid)
 RETURNS void LANGUAGE plpgsql AS $$
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
END $$;
ALTER TABLE object_upload_completions
 ADD COLUMN source_bucket_id uuid,
 ADD COLUMN source_copy_grant_id uuid,
 ADD CONSTRAINT object_copy_source_provenance CHECK(
  (source_bucket_id IS NULL AND source_copy_grant_id IS NULL) OR
  (source_bucket_id IS NOT NULL AND source_copy_grant_id IS NOT NULL AND origin='gateway_copy' AND
   source_bucket_id<>bucket_id AND source_bucket_id<>'00000000-0000-0000-0000-000000000000' AND
   source_copy_grant_id<>'00000000-0000-0000-0000-000000000000'));
CREATE FUNCTION protect_object_copy_receipt() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='UPDATE' AND OLD.source_bucket_id IS NOT NULL AND
  (NEW.id,NEW.account_id,NEW.app_id,NEW.bucket_id,NEW.subject_id,NEW.origin,NEW.object_key,NEW.bytes) IS DISTINCT FROM
  (OLD.id,OLD.account_id,OLD.app_id,OLD.bucket_id,OLD.subject_id,OLD.origin,OLD.object_key,OLD.bytes) THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_copy_source_fenced',MESSAGE='Cross-copy receipt ownership is immutable';
 END IF;
 IF TG_OP='UPDATE' AND (NEW.source_bucket_id,NEW.source_copy_grant_id,NEW.source_key,NEW.source_etag)
  IS DISTINCT FROM (OLD.source_bucket_id,OLD.source_copy_grant_id,OLD.source_key,OLD.source_etag) THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_copy_source_fenced',MESSAGE='Copy receipt source is immutable';
 END IF;
 IF NEW.source_bucket_id IS NOT NULL AND (TG_OP='INSERT' OR
  (OLD.write_phase='prepared' AND NEW.write_phase='dispatched')) THEN
  PERFORM assert_object_copy_source_authority(NEW.account_id,NEW.bucket_id,NEW.subject_id,
   NEW.source_bucket_id,NEW.source_key,NEW.source_copy_grant_id);
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER object_copy_receipt_bound BEFORE INSERT OR UPDATE ON object_upload_completions
 FOR EACH ROW EXECUTE FUNCTION protect_object_copy_receipt();

ALTER TABLE object_storage_multipart_part_grants
 ADD COLUMN source_bucket_id uuid,
 ADD COLUMN source_copy_grant_id uuid,
 ADD COLUMN source_subject_id text NOT NULL DEFAULT '',
 ADD COLUMN source_key text NOT NULL DEFAULT '',
 ADD CONSTRAINT object_multipart_copy_source_provenance CHECK(
 (source_bucket_id IS NULL AND source_copy_grant_id IS NULL AND source_subject_id='' AND source_key='') OR
 (source_bucket_id IS NOT NULL AND source_copy_grant_id IS NOT NULL AND source_subject_id<>'' AND source_key<>''));
CREATE FUNCTION protect_object_multipart_copy_source() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE u object_storage_multipart_uploads;
BEGIN
 IF TG_OP='UPDATE' AND
  (NEW.source_bucket_id,NEW.source_copy_grant_id,NEW.source_subject_id,NEW.source_key) IS DISTINCT FROM
  (OLD.source_bucket_id,OLD.source_copy_grant_id,OLD.source_subject_id,OLD.source_key) AND
  (NEW.transfer_token IS NULL OR NEW.transfer_token IS NOT DISTINCT FROM OLD.transfer_token) THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_copy_source_fenced',MESSAGE='Dispatched part copy source is immutable';
 END IF;
 IF NEW.source_bucket_id IS NOT NULL AND (TG_OP='INSERT' OR NEW.transfer_token IS DISTINCT FROM OLD.transfer_token) AND NEW.transfer_token IS NOT NULL THEN
  SELECT * INTO u FROM object_storage_multipart_uploads WHERE id=NEW.upload_id;
  IF u.id IS NULL OR u.state<>'active' OR u.expires_at<=clock_timestamp() OR NEW.source_bucket_id=u.bucket_id THEN
   RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_copy_source_fenced',MESSAGE='Copy destination session is unavailable';
  END IF;
  PERFORM assert_object_copy_source_authority(u.account_id,u.bucket_id,NEW.source_subject_id,
   NEW.source_bucket_id,NEW.source_key,NEW.source_copy_grant_id);
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER object_multipart_copy_source_bound BEFORE INSERT OR UPDATE ON object_storage_multipart_part_grants
 FOR EACH ROW EXECUTE FUNCTION protect_object_multipart_copy_source();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM object_storage_multipart_part_grants WHERE source_bucket_id IS NOT NULL AND transfer_token IS NOT NULL) OR
 EXISTS(SELECT 1 FROM object_upload_completions WHERE source_bucket_id IS NOT NULL AND write_phase IN ('prepared','dispatched')) OR
 EXISTS(SELECT 1 FROM object_storage_write_admissions w JOIN object_upload_completions c ON c.id=w.id
  WHERE c.source_bucket_id IS NOT NULL AND w.state='pending') THEN
  RAISE EXCEPTION 'Drain cross-bucket copy receipts before rollback';
 END IF;
 IF EXISTS(SELECT 1 FROM object_s3_copy_source_grants) THEN
  RAISE EXCEPTION 'Remove copy source grants before rollback';
 END IF;
END $$;
DROP TRIGGER object_copy_receipt_bound ON object_upload_completions;
DROP FUNCTION protect_object_copy_receipt();
ALTER TABLE object_upload_completions DROP CONSTRAINT object_copy_source_provenance,
 DROP COLUMN source_bucket_id,DROP COLUMN source_copy_grant_id;
DROP TRIGGER object_multipart_copy_source_bound ON object_storage_multipart_part_grants;
DROP FUNCTION protect_object_multipart_copy_source();
ALTER TABLE object_storage_multipart_part_grants DROP CONSTRAINT object_multipart_copy_source_provenance,
 DROP COLUMN source_bucket_id,DROP COLUMN source_copy_grant_id,DROP COLUMN source_subject_id,DROP COLUMN source_key;
DROP FUNCTION assert_object_copy_source_authority(uuid,uuid,text,uuid,text,uuid);
DROP TABLE object_s3_copy_source_grants;
DROP FUNCTION protect_object_s3_copy_source_grant();
DROP FUNCTION record_object_s3_copy_source_epoch();
DROP TABLE object_s3_copy_source_epochs;
DROP FUNCTION protect_object_s3_copy_source_epoch();
-- +goose StatementEnd
