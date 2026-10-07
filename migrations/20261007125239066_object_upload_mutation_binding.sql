-- +goose Up
-- ADR-590: create one pinned provider receipt with each new tracked upload.
-- Existing unknown receipts are not adopted or removed.
ALTER TABLE object_bucket_mutations ADD COLUMN upload_id uuid UNIQUE REFERENCES object_upload_completions(id) ON DELETE RESTRICT;
-- +goose StatementBegin
CREATE FUNCTION guard_bound_upload_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE u object_upload_completions%ROWTYPE;
BEGIN
 IF TG_OP='UPDATE' THEN
  IF OLD.upload_id IS NOT NULL OR NEW.upload_id IS NOT NULL THEN
   RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_upload_mutation_immutable',MESSAGE='Original upload receipts are immutable';
  END IF;
  RETURN NEW;
 END IF;
 IF TG_OP='DELETE' THEN
  IF OLD.upload_id IS NOT NULL AND EXISTS(SELECT 1 FROM object_upload_completions WHERE id=OLD.upload_id AND (write_phase<>'settled' OR status NOT IN ('completed','failed'))) THEN
   RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_upload_mutation_unsettled',MESSAGE='Settle the original upload before removing its provider receipt';
  END IF;
  RETURN OLD;
 END IF;
 IF NEW.upload_id IS NOT NULL THEN
  IF current_setting('transaction_isolation')<>'read committed' THEN
   RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_upload_mutation_isolation',MESSAGE='Upload receipt binding requires READ COMMITTED';
  END IF;
  SELECT * INTO u FROM object_upload_completions WHERE id=NEW.upload_id;
  PERFORM id FROM accounts WHERE id=u.account_id FOR UPDATE;
  PERFORM id FROM object_buckets WHERE id=u.bucket_id FOR UPDATE;
  SELECT * INTO u FROM object_upload_completions WHERE id=NEW.upload_id FOR UPDATE;
  IF EXISTS(SELECT 1 FROM object_bucket_write_fences WHERE bucket_id=u.bucket_id) THEN
   RAISE EXCEPTION USING ERRCODE='55000',CONSTRAINT='object_upload_capture_fenced',MESSAGE='Capture fences new receipt binding';
  END IF;
  IF u.id IS NULL OR u.bucket_id<>NEW.bucket_id OR u.status<>'pending' OR u.write_phase<>'prepared' OR NEW.kind<>'request' OR NOT EXISTS(SELECT 1 FROM object_buckets WHERE id=u.bucket_id AND backend_id=NEW.backend_id AND backend_fingerprint=NEW.backend_fingerprint AND physical_name=NEW.physical_name AND state='ready') THEN
   RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_upload_mutation_original',MESSAGE='Bind only the original prepared upload placement';
  END IF;
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER object_upload_mutation_guard BEFORE INSERT OR UPDATE OR DELETE ON object_bucket_mutations FOR EACH ROW EXECUTE FUNCTION guard_bound_upload_mutation();
-- +goose StatementBegin
CREATE FUNCTION compose_object_upload_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='INSERT' AND NEW.write_phase='prepared' AND NEW.status='pending' THEN
  INSERT INTO object_bucket_mutations(id,bucket_id,kind,backend_id,backend_fingerprint,physical_name,upload_id)
  SELECT NEW.id,id,'request',backend_id,backend_fingerprint,physical_name,NEW.id FROM object_buckets WHERE id=NEW.bucket_id;
 ELSIF TG_OP='UPDATE' AND NEW.write_phase='settled' AND NEW.status IN ('completed','failed') THEN
  DELETE FROM object_bucket_mutations WHERE upload_id=NEW.id;
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER object_upload_mutation_composition AFTER INSERT OR UPDATE ON object_upload_completions FOR EACH ROW EXECUTE FUNCTION compose_object_upload_mutation();

-- +goose StatementBegin
CREATE FUNCTION protect_bound_upload_journal() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF EXISTS(SELECT 1 FROM object_bucket_mutations WHERE upload_id=OLD.id) AND
  ((to_jsonb(NEW)-ARRAY['route_id','status','etag','error_code','write_phase','recovery_token','recovery_lease_until','recovery_retry_at','recovery_cursor','recovery_versions_observed','version_id','protection_verified','protection_dispatched','encryption_dispatched','encryption_verified']) IS DISTINCT FROM
   (to_jsonb(OLD)-ARRAY['route_id','status','etag','error_code','write_phase','recovery_token','recovery_lease_until','recovery_retry_at','recovery_cursor','recovery_versions_observed','version_id','protection_verified','protection_dispatched','encryption_dispatched','encryption_verified']) OR
   (NEW.route_id IS NOT NULL AND NEW.route_id IS DISTINCT FROM OLD.route_id)) THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_upload_bound_journal_immutable',MESSAGE='An original provider receipt cannot be reassigned to a different upload intent';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER object_upload_bound_journal BEFORE UPDATE ON object_upload_completions FOR EACH ROW EXECUTE FUNCTION protect_bound_upload_journal();

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM object_bucket_mutations WHERE upload_id IS NOT NULL) OR EXISTS(SELECT 1 FROM object_bucket_write_fences) THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_upload_mutation_down_busy',MESSAGE='Settle bound uploads and release capture holds before removing receipt ownership';
 END IF;
END $$;
-- +goose StatementEnd
DROP TRIGGER object_upload_bound_journal ON object_upload_completions;
DROP FUNCTION protect_bound_upload_journal();
DROP TRIGGER object_upload_mutation_composition ON object_upload_completions;
DROP FUNCTION compose_object_upload_mutation();
DROP TRIGGER object_upload_mutation_guard ON object_bucket_mutations;
DROP FUNCTION guard_bound_upload_mutation();
ALTER TABLE object_bucket_mutations DROP COLUMN upload_id;
