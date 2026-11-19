-- migration-compat: expand
-- ADR-20261119065000: a customer chooses Object Lock when creating a bucket.
-- The reservation journals the accepted enablement request while the bucket
-- is still provisioning, so provisioning and recovery create the provider
-- bucket with ObjectLockEnabledForBucket. Every other journal write still
-- requires a ready bucket.
-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION protect_object_bucket_object_lock() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE b object_buckets;
BEGIN
 SELECT * INTO b FROM object_buckets WHERE id=coalesce(NEW.bucket_id,OLD.bucket_id) FOR NO KEY UPDATE;
 IF TG_OP='DELETE' THEN
  IF FOUND AND b.state<>'deleted' THEN RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Preserve permanent Object Lock history until physical bucket deletion'; END IF;
  RETURN OLD;
 END IF;
 -- A bucket created with Object Lock journals its accepted request while it
 -- is provisioning (ADR-20261119065000). Only that first, waiting request
 -- may be inserted before the bucket is ready.
 IF NOT FOUND OR (NEW.account_id,NEW.app_id) IS DISTINCT FROM (b.account_id,b.app_id) OR (b.state<>'ready' AND NOT
  (TG_OP='INSERT' AND b.state='provisioning' AND NEW.enabled_required AND NEW.state='waiting' AND NOT NEW.dispatched AND NEW.revision=1)) THEN
  RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Object Lock configuration requires an owned ready bucket';
 END IF;
 IF TG_OP='INSERT' THEN
  IF NEW.revision>1 OR NEW.state='applying' OR NEW.dispatched THEN
   RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Object Lock requires an initial observation or durable request';
  END IF;
 ELSE
  IF (NEW.bucket_id,NEW.account_id,NEW.app_id) IS DISTINCT FROM (OLD.bucket_id,OLD.account_id,OLD.app_id)
   OR (OLD.enabled_required AND NOT NEW.enabled_required) OR (OLD.native_enabled_observed AND NOT NEW.native_enabled_observed)
   OR NEW.revision<OLD.revision OR NEW.revision>OLD.revision+1
   OR (NEW.revision=OLD.revision AND (NEW.desired_snapshot IS DISTINCT FROM OLD.desired_snapshot OR (OLD.dispatched AND NOT NEW.dispatched)))
   OR (NEW.revision<>OLD.revision AND (NEW.state<>'waiting' OR NEW.dispatched OR
    (OLD.state<>'ready' AND NOT (OLD.revision=0 AND OLD.desired_snapshot='null' AND (OLD.lease_until IS NULL OR OLD.lease_until<=clock_timestamp())))))
   OR (NEW.state='applying' AND NEW.lease_token IS DISTINCT FROM OLD.lease_token AND
    ((OLD.lease_until IS NOT NULL AND OLD.lease_until>clock_timestamp()) OR OLD.retry_at>clock_timestamp()))
   OR (OLD.state<>'ready' AND NEW.state='ready' AND NOT
    (OLD.state='applying' AND OLD.lease_until>clock_timestamp() AND NEW.revision=OLD.revision))
   OR (NOT OLD.dispatched AND NEW.dispatched AND NOT
    (OLD.state='applying' AND NEW.state='applying' AND OLD.lease_token=NEW.lease_token AND OLD.lease_until>clock_timestamp())) THEN
   RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Object Lock intent, enablement history and leased progress are immutable';
  END IF;
 END IF;
 IF (NEW.enabled_required OR NEW.desired_snapshot<>'null' OR NOT NEW.observed_known) AND
  (NEW.state='applying' OR NEW.state='ready' AND (TG_OP='INSERT' OR OLD.state<>'ready')) AND
  (NOT object_lock_drained(NEW.bucket_id) OR NEW.enabled_required AND NOT object_lock_versioning_ready(NEW.bucket_id)) THEN
  RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Object Lock requires drained transfers and verified Enabled versioning';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION protect_object_bucket_object_lock() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE b object_buckets;
BEGIN
 SELECT * INTO b FROM object_buckets WHERE id=coalesce(NEW.bucket_id,OLD.bucket_id) FOR NO KEY UPDATE;
 IF TG_OP='DELETE' THEN
  IF FOUND AND b.state<>'deleted' THEN RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Preserve permanent Object Lock history until physical bucket deletion'; END IF;
  RETURN OLD;
 END IF;
 IF NOT FOUND OR b.state<>'ready' OR (NEW.account_id,NEW.app_id) IS DISTINCT FROM (b.account_id,b.app_id) THEN
  RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Object Lock configuration requires an owned ready bucket';
 END IF;
 IF TG_OP='INSERT' THEN
  IF NEW.revision>1 OR NEW.state='applying' OR NEW.dispatched THEN
   RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Object Lock requires an initial observation or durable request';
  END IF;
 ELSE
  IF (NEW.bucket_id,NEW.account_id,NEW.app_id) IS DISTINCT FROM (OLD.bucket_id,OLD.account_id,OLD.app_id)
   OR (OLD.enabled_required AND NOT NEW.enabled_required) OR (OLD.native_enabled_observed AND NOT NEW.native_enabled_observed)
   OR NEW.revision<OLD.revision OR NEW.revision>OLD.revision+1
   OR (NEW.revision=OLD.revision AND (NEW.desired_snapshot IS DISTINCT FROM OLD.desired_snapshot OR (OLD.dispatched AND NOT NEW.dispatched)))
   OR (NEW.revision<>OLD.revision AND (NEW.state<>'waiting' OR NEW.dispatched OR
    (OLD.state<>'ready' AND NOT (OLD.revision=0 AND OLD.desired_snapshot='null' AND (OLD.lease_until IS NULL OR OLD.lease_until<=clock_timestamp())))))
   OR (NEW.state='applying' AND NEW.lease_token IS DISTINCT FROM OLD.lease_token AND
    ((OLD.lease_until IS NOT NULL AND OLD.lease_until>clock_timestamp()) OR OLD.retry_at>clock_timestamp()))
   OR (OLD.state<>'ready' AND NEW.state='ready' AND NOT
    (OLD.state='applying' AND OLD.lease_until>clock_timestamp() AND NEW.revision=OLD.revision))
   OR (NOT OLD.dispatched AND NEW.dispatched AND NOT
    (OLD.state='applying' AND NEW.state='applying' AND OLD.lease_token=NEW.lease_token AND OLD.lease_until>clock_timestamp())) THEN
   RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Object Lock intent, enablement history and leased progress are immutable';
  END IF;
 END IF;
 IF (NEW.enabled_required OR NEW.desired_snapshot<>'null' OR NOT NEW.observed_known) AND
  (NEW.state='applying' OR NEW.state='ready' AND (TG_OP='INSERT' OR OLD.state<>'ready')) AND
  (NOT object_lock_drained(NEW.bucket_id) OR NEW.enabled_required AND NOT object_lock_versioning_ready(NEW.bucket_id)) THEN
  RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Object Lock requires drained transfers and verified Enabled versioning';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
