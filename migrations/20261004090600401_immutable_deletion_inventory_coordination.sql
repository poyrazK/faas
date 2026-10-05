-- +goose Up
ALTER TABLE object_deletions ADD COLUMN IF NOT EXISTS target_provider_version_id text NOT NULL DEFAULT '' CHECK(octet_length(target_provider_version_id)<=1024);
ALTER TABLE object_deletions ADD COLUMN IF NOT EXISTS recovery_claimed boolean NOT NULL DEFAULT false;
ALTER TABLE object_deletions DROP CONSTRAINT IF EXISTS object_deletions_selector_check;
-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='object_deletions'::regclass AND conname='object_deletions_selector_check') THEN
  ALTER TABLE object_deletions ADD CONSTRAINT object_deletions_selector_check CHECK(selector IN ('','null') OR selector ~ '^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$');
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='object_deletions'::regclass AND conname='object_deletions_target_identity') THEN
  ALTER TABLE object_deletions ADD CONSTRAINT object_deletions_target_identity CHECK(
 (selector IN ('','null') AND target_provider_version_id='') OR
 (selector NOT IN ('','null') AND target_provider_version_id NOT IN ('','null') AND provider_status='' AND baseline='[]' AND reserved_bytes=0)
);
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='object_deletions'::regclass AND conname='object_deletions_target_completion') THEN
  ALTER TABLE object_deletions ADD CONSTRAINT object_deletions_target_completion CHECK(state<>'completed' OR selector IN ('','null') OR (version_id=selector AND provider_version_id=target_provider_version_id));
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION protect_object_deletion() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.id<>OLD.id OR NEW.bucket_id<>OLD.bucket_id OR NEW.object_key<>OLD.object_key OR NEW.selector<>OLD.selector
  OR NEW.created_at<>OLD.created_at OR NEW.provider_status<>OLD.provider_status OR NEW.reserved_bytes<>OLD.reserved_bytes
  OR NEW.target_provider_version_id<>OLD.target_provider_version_id
  OR (OLD.recovery_claimed AND NOT NEW.recovery_claimed)
  OR (OLD.state<>'prepared' AND NEW.baseline<>OLD.baseline)
  OR (OLD.state='dispatched' AND NEW.state NOT IN ('dispatched','completed') AND NOT (NEW.state='failed' AND NEW.last_error_code='provider_rejected' AND NOT OLD.recovery_claimed))
  OR OLD.state IN ('completed','failed') THEN
  RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Deletion identity and dispatched attempt are immutable';
 END IF;
 RETURN NEW;
END $$;
CREATE OR REPLACE FUNCTION fence_immutable_object_deletion() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.selector IN ('','null') THEN RETURN NEW; END IF;
 -- Match the service's bucket-before-account admission order. The durable
 -- capacity intent owns this lock before any provider page is requested.
 PERFORM 1 FROM object_buckets WHERE id=NEW.bucket_id AND state='ready' FOR NO KEY UPDATE;
 IF NOT FOUND OR NOT EXISTS(SELECT 1 FROM object_version_references v WHERE v.id::text=NEW.selector AND v.bucket_id=NEW.bucket_id AND v.object_key=NEW.object_key AND v.native_version_id=NEW.target_provider_version_id AND v.native_version_id<>'null') THEN
  RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Immutable deletion requires the exact owned version reference';
 END IF;
 IF EXISTS(SELECT 1 FROM object_storage_capacity_reconciliations c WHERE c.bucket_id=NEW.bucket_id AND c.state IN ('waiting','scanning'))
  OR EXISTS(SELECT 1 FROM object_bucket_versioning v WHERE v.bucket_id=NEW.bucket_id AND v.state<>'ready') THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_deletion_fenced',MESSAGE='Inventory and configuration exclude immutable deletion';
 END IF;
 RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS object_immutable_deletion_fence ON object_deletions;
CREATE TRIGGER object_immutable_deletion_fence BEFORE INSERT ON object_deletions FOR EACH ROW EXECUTE FUNCTION fence_immutable_object_deletion();
-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN IF EXISTS(SELECT 1 FROM object_deletions WHERE selector NOT IN ('','null')) THEN RAISE EXCEPTION 'Cannot discard immutable deletion identities or receipts'; END IF; END $$;
DROP TRIGGER object_immutable_deletion_fence ON object_deletions;
DROP FUNCTION fence_immutable_object_deletion();
CREATE OR REPLACE FUNCTION protect_object_deletion() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.id<>OLD.id OR NEW.bucket_id<>OLD.bucket_id OR NEW.object_key<>OLD.object_key OR NEW.selector<>OLD.selector
  OR NEW.created_at<>OLD.created_at OR NEW.provider_status<>OLD.provider_status OR NEW.reserved_bytes<>OLD.reserved_bytes
  OR (OLD.state<>'prepared' AND NEW.baseline<>OLD.baseline)
  OR (OLD.state='dispatched' AND NEW.state NOT IN ('dispatched','completed') AND NOT (NEW.state='failed' AND NEW.last_error_code='provider_rejected'))
  OR OLD.state IN ('completed','failed') THEN
  RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Deletion identity and dispatched attempt are immutable';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
ALTER TABLE object_deletions DROP CONSTRAINT object_deletions_target_completion;
ALTER TABLE object_deletions DROP CONSTRAINT object_deletions_target_identity;
ALTER TABLE object_deletions DROP CONSTRAINT object_deletions_selector_check;
ALTER TABLE object_deletions ADD CONSTRAINT object_deletions_selector_check CHECK(selector IN ('','null'));
ALTER TABLE object_deletions DROP COLUMN target_provider_version_id;
ALTER TABLE object_deletions DROP COLUMN recovery_claimed;
