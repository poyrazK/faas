-- filename: 20261105193328820_object_fence_no_key_update.sql
-- migration-compat: expand

-- +goose Up
-- The object-storage fences serialize object writers of one account on its
-- resource namespace row. FOR UPDATE also conflicts with the FOR KEY SHARE
-- that every foreign-key insert naming the namespace takes, so one slow object
-- job held up request_id_journal and invocation inserts for the whole account
-- and filled apid's pool (CP stalls 2026-11-05). FOR NO KEY UPDATE still
-- excludes every other fence, every FOR UPDATE holder and status updates, and
-- lets plain foreign-key inserts through. The receipt guards and the bucket
-- reservation fence lock the account row the same way. Only the lock mode
-- changes.
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.fence_object_bucket_account_cleanup() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE current_status text; owner_org uuid; adopted boolean;
BEGIN
 SELECT o.id,NOT o.personal_org AND o.resource_ownership_enabled INTO owner_org,adopted
 FROM apps a JOIN orgs o ON o.id=a.org_id WHERE a.id=NEW.app_id AND a.account_id=NEW.account_id;
 IF adopted THEN
  SELECT CASE WHEN deleted_pending THEN 'deleted_pending' ELSE status END INTO current_status FROM orgs WHERE id=owner_org FOR UPDATE;
 ELSE
  SELECT status INTO current_status FROM accounts WHERE id=NEW.account_id FOR NO KEY UPDATE;
 END IF;
 IF current_status IS DISTINCT FROM 'active' THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_bucket_account_cleanup_fenced',MESSAGE='Inactive storage owner cannot reserve new object buckets';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.guard_bound_multipart_mutation() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE u object_storage_multipart_uploads%ROWTYPE;
BEGIN
 IF TG_OP='UPDATE' THEN
  IF OLD.multipart_upload_id IS NOT NULL OR NEW.multipart_upload_id IS NOT NULL THEN
   RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_multipart_mutation_immutable',MESSAGE='Original multipart receipts are immutable';
  END IF;
  RETURN NEW;
 END IF;
 IF TG_OP='DELETE' THEN
  IF OLD.multipart_upload_id IS NOT NULL AND EXISTS(SELECT 1 FROM object_storage_multipart_uploads WHERE id=OLD.multipart_upload_id AND state NOT IN ('completed','aborted')) THEN
   RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_multipart_mutation_unsettled',MESSAGE='Settle the original session before removing its provider receipt';
  END IF;
  RETURN OLD;
 END IF;
 IF NEW.multipart_upload_id IS NOT NULL THEN
  IF current_setting('transaction_isolation')<>'read committed' THEN
   RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_multipart_mutation_isolation',MESSAGE='Multipart receipt binding requires READ COMMITTED';
  END IF;
  SELECT * INTO u FROM object_storage_multipart_uploads WHERE id=NEW.multipart_upload_id;
  PERFORM id FROM accounts WHERE id=u.account_id FOR NO KEY UPDATE;
  PERFORM id FROM object_buckets WHERE id=u.bucket_id FOR UPDATE;
  SELECT * INTO u FROM object_storage_multipart_uploads WHERE id=NEW.multipart_upload_id FOR UPDATE;
  IF EXISTS(SELECT 1 FROM object_bucket_write_fences WHERE bucket_id=u.bucket_id) THEN
   RAISE EXCEPTION USING ERRCODE='55000',CONSTRAINT='object_multipart_capture_fenced',MESSAGE='Capture fences new receipt binding';
  END IF;
  IF u.id IS NULL OR u.bucket_id<>NEW.bucket_id OR u.state<>'initiating' OR u.provider_upload_id<>'' OR NEW.kind<>'request' OR NOT EXISTS(SELECT 1 FROM object_buckets WHERE id=u.bucket_id AND account_id=u.account_id AND app_id=u.app_id AND backend_id=NEW.backend_id AND backend_fingerprint=NEW.backend_fingerprint AND physical_name=NEW.physical_name AND state='ready') THEN
   RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_multipart_mutation_original',MESSAGE='Bind only the original reserved session placement';
  END IF;
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.guard_bound_upload_mutation() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
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
  PERFORM id FROM accounts WHERE id=u.account_id FOR NO KEY UPDATE;
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
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.fence_object_lifecycle_deletion() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE aid uuid; allowed boolean;
BEGIN
 IF TG_OP='UPDATE' AND (NEW.lifecycle_scan_id IS DISTINCT FROM OLD.lifecycle_scan_id OR NEW.lifecycle_binding<>OLD.lifecycle_binding) THEN
  RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Lifecycle deletion identity is immutable';
 END IF;
 IF NEW.lifecycle_scan_id IS NULL THEN RETURN NEW; END IF;
 IF TG_OP='UPDATE' AND NOT (OLD.state='prepared' AND NEW.state='dispatched') THEN RETURN NEW; END IF;
 -- Both the API store and this defensive trigger follow bucket-before-account
 -- lock order. Re-read the lease and revision after acquiring those locks.
 SELECT account_id INTO aid FROM object_buckets WHERE id=NEW.bucket_id AND state='ready' FOR NO KEY UPDATE;
 IF aid IS NULL THEN RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Lifecycle deletion requires a ready owned bucket'; END IF;
 PERFORM 1 FROM resource_namespaces WHERE id=aid FOR NO KEY UPDATE;
 SELECT EXISTS(
  SELECT 1 FROM object_lifecycle_scans s JOIN object_bucket_lifecycle p ON p.bucket_id=s.bucket_id,
   LATERAL jsonb_array_elements(s.rules) r
  WHERE s.id=NEW.lifecycle_scan_id AND s.bucket_id=NEW.bucket_id AND s.revision=p.revision AND s.state='scanning'
   AND s.lease_token=NEW.lifecycle_binding->>'scan_token' AND s.lease_until>clock_timestamp()
   AND r->>'id'=NEW.lifecycle_binding->>'rule_id' AND r->>'status'='Enabled'
   AND left(NEW.object_key,char_length(coalesce(r->'filter'->>'prefix','')))=coalesce(r->'filter'->>'prefix','')
   AND ((NEW.lifecycle_binding->>'kind'='current' AND (r->'expiration' ? 'days' OR r->'expiration' ? 'date'))
    OR (NEW.lifecycle_binding->>'kind'='noncurrent' AND r ? 'noncurrent_version_expiration')
    OR (NEW.lifecycle_binding->>'kind'='expired_marker' AND (r->'expiration' ? 'days' OR r->'expiration' ? 'date' OR r->'expiration'->>'expired_object_delete_marker'='true')))
 ) INTO allowed;
 IF NOT allowed OR (NEW.lifecycle_binding->>'expected_last_modified')::timestamptz>clock_timestamp()
  OR (NEW.selector='null' AND NEW.lifecycle_binding->>'expected_provider_version_id'<>'null')
  OR (NEW.selector NOT IN ('','null') AND NEW.lifecycle_binding->>'expected_provider_version_id'<>coalesce(to_jsonb(NEW)->>'target_provider_version_id','')) THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_lifecycle_deletion_fenced',MESSAGE='A live matching lifecycle scan and target are required before dispatch';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.fence_object_lifecycle_multipart() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE aid uuid; allowed boolean;
BEGIN
 IF TG_OP='INSERT' THEN
  IF NEW.lifecycle_scan_id IS NOT NULL OR NEW.lifecycle_binding<>'{}' THEN
   RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Multipart lifecycle admission requires an existing active upload';
  END IF;
  RETURN NEW;
 END IF;
 IF OLD.lifecycle_scan_id IS NOT NULL THEN
  IF NEW.lifecycle_scan_id IS DISTINCT FROM OLD.lifecycle_scan_id OR NEW.lifecycle_binding<>OLD.lifecycle_binding
   OR NEW.id<>OLD.id OR NEW.account_id<>OLD.account_id OR NEW.app_id<>OLD.app_id OR NEW.bucket_id<>OLD.bucket_id
   OR NEW.object_key<>OLD.object_key OR NEW.provider_upload_id<>OLD.provider_upload_id OR NEW.created_at<>OLD.created_at THEN
   RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Multipart lifecycle admission identity is immutable';
  END IF;
  RETURN NEW;
 END IF;
 IF NEW.lifecycle_scan_id IS NULL THEN RETURN NEW; END IF;
 IF OLD.state<>'active' OR NEW.state<>'aborting' OR NEW.lease_until IS NOT NULL
  OR OLD.lease_until>clock_timestamp() OR NEW.provider_upload_id<>OLD.provider_upload_id OR NEW.created_at<>OLD.created_at
  OR NEW.id<>OLD.id OR NEW.account_id<>OLD.account_id OR NEW.app_id<>OLD.app_id OR NEW.bucket_id<>OLD.bucket_id OR NEW.object_key<>OLD.object_key
  OR (NEW.lifecycle_binding->>'expected_created_at')::timestamptz<>OLD.created_at THEN
  RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Multipart lifecycle admission requires the unchanged active upload';
 END IF;
 -- The store takes these locks before the upload row. Direct SQL callers must
 -- follow the same bucket/account/upload ordering to avoid deadlocks.
 SELECT account_id INTO aid FROM object_buckets WHERE id=NEW.bucket_id AND account_id=NEW.account_id AND app_id=NEW.app_id AND state='ready' FOR NO KEY UPDATE;
 IF aid IS NULL THEN RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Multipart lifecycle admission requires an owned ready bucket'; END IF;
 PERFORM 1 FROM resource_namespaces WHERE id=aid FOR NO KEY UPDATE;
 SELECT EXISTS(
  SELECT 1 FROM object_lifecycle_scans s JOIN object_bucket_lifecycle p ON p.bucket_id=s.bucket_id,
   LATERAL jsonb_array_elements(s.rules) r
  WHERE s.id=NEW.lifecycle_scan_id AND s.bucket_id=NEW.bucket_id AND s.revision=p.revision AND s.state='scanning' AND s.phase='multipart'
   AND s.created_at>=NEW.created_at AND (s.last_upload_id IS NULL OR NEW.id>s.last_upload_id)
   AND s.lease_token=NEW.lifecycle_binding->>'scan_token' AND s.lease_until>clock_timestamp()
   AND r->>'id'=NEW.lifecycle_binding->>'rule_id' AND r->>'status'='Enabled'
   AND left(NEW.object_key,char_length(coalesce(r->'filter'->>'prefix','')))=coalesce(r->'filter'->>'prefix','')
   AND coalesce(r->'filter'->'tags','{}')='{}' AND (r->>'abort_incomplete_multipart_days')::bigint>0
   -- Compare elapsed UTC days instead of constructing an out-of-range date
   -- for an otherwise valid large days value.
   AND (clock_timestamp() AT TIME ZONE 'UTC')::date-(NEW.created_at AT TIME ZONE 'UTC')::date > (r->>'abort_incomplete_multipart_days')::bigint
 ) INTO allowed;
 IF NOT allowed THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_lifecycle_multipart_fenced',MESSAGE='A live matching lifecycle scan and aged upload are required before admission';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.fence_object_lifecycle_protection() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE aid uuid; required boolean;
BEGIN
 IF TG_OP='INSERT' THEN
  IF NEW.protection_verified OR NEW.deletion_verified OR NEW.protection_required AND NEW.state<>'prepared' THEN
   RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Protected lifecycle intents must begin prepared and unverified';
  END IF;
  SELECT account_id INTO aid FROM object_buckets WHERE id=NEW.bucket_id;
  PERFORM 1 FROM resource_namespaces WHERE id=aid FOR NO KEY UPDATE;
  PERFORM 1 FROM object_buckets WHERE id=NEW.bucket_id FOR NO KEY UPDATE;
  SELECT NEW.lifecycle_scan_id IS NOT NULL AND NEW.selector<>'' AND EXISTS(
   SELECT 1 FROM object_bucket_object_lock l WHERE l.bucket_id=NEW.bucket_id
    AND (l.enabled_required OR l.native_enabled_observed OR l.observed_snapshot->>'enabled'='true')
  ) INTO required;
  IF NEW.protection_required IS DISTINCT FROM required OR required AND jsonb_typeof(NEW.lifecycle_binding->'expected_delete_marker') IS DISTINCT FROM 'boolean' THEN
   RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Protected lifecycle deletion requires a frozen target classification';
  END IF;
 ELSE
  IF NEW.protection_required<>OLD.protection_required OR OLD.protection_verified AND NOT NEW.protection_verified OR OLD.deletion_verified AND NOT NEW.deletion_verified THEN
   RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Lifecycle protection evidence is immutable';
  END IF;
  IF NOT OLD.protection_verified AND NEW.protection_verified AND NOT (NEW.protection_required AND OLD.state='prepared' AND NEW.state='dispatched') THEN
   RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Protection proof must accompany initial dispatch';
  END IF;
  IF NOT OLD.deletion_verified AND NEW.deletion_verified AND NOT (OLD.state='dispatched' AND NEW.state='completed') THEN
   RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Absence proof must accompany completion';
  END IF;
  IF NEW.last_error_code='object_protected' AND NOT (OLD.state='prepared' AND NEW.state='failed') AND OLD.last_error_code<>'object_protected' THEN
   RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='A dispatched uncertain attempt cannot be dismissed as protected';
  END IF;
  IF OLD.state='prepared' AND NEW.state='dispatched' AND NEW.protection_required AND NOT NEW.protection_verified THEN
   RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Fresh native policy proof is required before lifecycle dispatch';
  END IF;
 END IF;
 IF NEW.protection_required AND NEW.state='completed' AND (
  NOT NEW.deletion_verified OR jsonb_typeof(NEW.lifecycle_binding->'expected_delete_marker') IS DISTINCT FROM 'boolean'
  OR NEW.delete_marker::text IS DISTINCT FROM NEW.lifecycle_binding->>'expected_delete_marker'
  OR NEW.provider_version_id IS DISTINCT FROM NEW.lifecycle_binding->>'expected_provider_version_id') THEN
  RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Exact lifecycle target absence proof is required before completion';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.fence_object_upload_capture_admission() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE bucket_state text;
BEGIN
 -- Fence queries need a fresh snapshot after waiting for the source lock.
 -- Reject old transaction snapshots rather than hiding a committed hold.
 IF current_setting('transaction_isolation') <> 'read committed' THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_upload_admission_isolation',MESSAGE='Upload admission requires READ COMMITTED';
 END IF;
 -- Match the application admission order: account before source bucket.
 -- An older replica's INSERT must not invert the account FK/row locks.
 PERFORM id FROM resource_namespaces WHERE id=NEW.account_id FOR NO KEY UPDATE;
 SELECT state INTO bucket_state FROM object_buckets
 WHERE id=NEW.bucket_id AND account_id=NEW.account_id AND app_id=NEW.app_id FOR UPDATE;
 IF bucket_state IS DISTINCT FROM 'ready' THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_upload_bucket_not_ready',MESSAGE='Bucket cleanup fences upload admission';
 END IF;
 IF EXISTS(SELECT 1 FROM object_bucket_write_fences WHERE bucket_id=NEW.bucket_id) THEN
  RAISE EXCEPTION USING ERRCODE='55000',CONSTRAINT='object_upload_capture_fenced',MESSAGE='Checkpoint capture fences new upload admission';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.fence_object_write_key() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE owner uuid; own_write uuid;
BEGIN
 SELECT account_id INTO owner FROM object_buckets WHERE id=NEW.bucket_id;
 PERFORM 1 FROM resource_namespaces WHERE id=owner FOR NO KEY UPDATE;
 PERFORM 1 FROM object_buckets WHERE id=NEW.bucket_id FOR SHARE;
 IF TG_TABLE_NAME='object_storage_write_admissions' THEN
  own_write:=NEW.id;
 ELSE
  own_write:=NEW.last_write_id;
  IF TG_OP='UPDATE' AND NEW.last_write_id IS NOT DISTINCT FROM OLD.last_write_id THEN own_write:=NULL; END IF;
 END IF;
 IF EXISTS(SELECT 1 FROM object_storage_write_admissions w
  WHERE w.bucket_id=NEW.bucket_id AND w.key_hash=NEW.key_hash AND w.state='pending'
  AND w.id IS DISTINCT FROM own_write
  AND (w.multipart_upload_id IS NULL OR EXISTS(SELECT 1 FROM object_storage_multipart_uploads m
   WHERE m.id=w.multipart_upload_id AND m.state IN ('initiating','active','completing','completing_conditional','aborting')))) THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_write_key_fenced',MESSAGE='Settle the pending write before replacing its proof';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.fence_untracked_object_lock_write() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
 IF TG_TABLE_NAME='object_storage_key_grants' THEN
  IF NEW.last_write_id IS NOT NULL THEN RETURN NEW; END IF;
 ELSE
  IF NEW.kind<>'proxy' OR NEW.route_receipt THEN RETURN NEW; END IF;
 END IF;
 PERFORM 1 FROM resource_namespaces a JOIN object_buckets b ON b.account_id=a.id WHERE b.id=NEW.bucket_id FOR NO KEY UPDATE OF a;
 PERFORM 1 FROM object_buckets WHERE id=NEW.bucket_id FOR NO KEY UPDATE;
 IF EXISTS(SELECT 1 FROM object_bucket_object_lock WHERE bucket_id=NEW.bucket_id AND (enabled_required OR native_enabled_observed OR state<>'ready')) THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_lock_admission_fenced',MESSAGE='Object Lock requires a tracked protection-aware write';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.guard_object_multipart_initiation_dispatch() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE u object_storage_multipart_uploads%ROWTYPE;
BEGIN
 IF TG_OP='DELETE' THEN
  IF EXISTS(SELECT 1 FROM object_bucket_mutations WHERE multipart_upload_id=OLD.multipart_upload_id) THEN
   RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_multipart_initiation_live',MESSAGE='Retain original initiation evidence while the provider receipt is outstanding';
  END IF;
  RETURN OLD;
 END IF;
 IF current_setting('transaction_isolation')<>'read committed' THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_multipart_initiation_isolation',MESSAGE='Initiation dispatch requires READ COMMITTED';
 END IF;
 SELECT * INTO u FROM object_storage_multipart_uploads WHERE id=NEW.multipart_upload_id;
 PERFORM id FROM resource_namespaces WHERE id=u.account_id FOR NO KEY UPDATE;
 PERFORM id FROM object_buckets WHERE id=u.bucket_id FOR UPDATE;
 SELECT * INTO u FROM object_storage_multipart_uploads WHERE id=NEW.multipart_upload_id FOR UPDATE;
 IF u.id IS NULL OR u.state<>'initiating' OR u.provider_upload_id<>'' OR NOT EXISTS(
  SELECT 1 FROM object_bucket_mutations m JOIN object_buckets b ON b.id=m.bucket_id
  WHERE m.multipart_upload_id=u.id AND m.kind='request' AND b.id=u.bucket_id AND b.account_id=u.account_id AND b.app_id=u.app_id
  AND b.state='ready' AND b.backend_id=m.backend_id AND b.backend_fingerprint=m.backend_fingerprint AND b.physical_name=m.physical_name
 ) THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_multipart_initiation_original',MESSAGE='Initiation requires the original session receipt and placement';
 END IF;
 IF TG_OP='INSERT' THEN
  IF NEW.dispatched OR NEW.dispatch_token<>'' OR NEW.provider_upload_id<>'' OR u.lease_token IS NOT NULL THEN
   RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_multipart_initiation_reserved',MESSAGE='Initialize dispatch evidence only with a new reservation';
  END IF;
 ELSIF NEW.multipart_upload_id<>OLD.multipart_upload_id OR u.lease_token IS NULL OR u.lease_token='' OR u.lease_until IS NULL OR u.lease_until<=clock_timestamp() OR NOT (
  (NOT OLD.dispatched AND NEW.dispatched AND NEW.dispatch_token=u.lease_token AND NEW.provider_upload_id='') OR
  (OLD.dispatched AND NEW.dispatched AND OLD.dispatch_token=u.lease_token AND NEW.dispatch_token=OLD.dispatch_token AND OLD.provider_upload_id='' AND btrim(NEW.provider_upload_id)<>'' AND NEW.provider_upload_id !~ '[[:cntrl:]]')
 ) THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_multipart_initiation_immutable',MESSAGE='Dispatch is once-only and only its live owner may record a positive reply';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.guard_object_multipart_part_writer() RETURNS trigger
    LANGUAGE plpgsql
    AS $_$
DECLARE u object_storage_multipart_uploads%ROWTYPE; g object_storage_multipart_part_grants%ROWTYPE;
BEGIN
 IF TG_OP='DELETE' THEN
  IF OLD.dispatched AND NOT OLD.settled THEN
   RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_multipart_part_writer_conflict',MESSAGE='Uncertain part evidence cannot be deleted';
  END IF;
  RETURN OLD;
 END IF;
 IF current_setting('transaction_isolation')<>'read committed' THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_multipart_part_writer_conflict',MESSAGE='Part writers require READ COMMITTED';
 END IF;
 SELECT * INTO u FROM object_storage_multipart_uploads WHERE id=NEW.upload_id;
 PERFORM id FROM resource_namespaces WHERE id=u.account_id FOR NO KEY UPDATE;
 PERFORM id FROM object_buckets WHERE id=u.bucket_id FOR UPDATE;
 SELECT * INTO u FROM object_storage_multipart_uploads WHERE id=NEW.upload_id FOR UPDATE;
 SELECT * INTO g FROM object_storage_multipart_part_grants WHERE upload_id=NEW.upload_id AND part_number=NEW.part_number;
 IF u.id IS NULL OR u.bucket_id<>NEW.bucket_id OR NOT EXISTS(SELECT 1 FROM object_buckets b WHERE b.id=u.bucket_id AND b.account_id=u.account_id AND b.app_id=u.app_id AND b.state='ready' AND b.backend_id=NEW.backend_id AND b.backend_fingerprint=NEW.backend_fingerprint AND b.physical_name=NEW.physical_name) THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_multipart_part_writer_conflict',MESSAGE='Part writer requires its original placement';
 END IF;
 IF TG_OP='INSERT' THEN
  IF EXISTS(SELECT 1 FROM object_bucket_write_fences WHERE bucket_id=u.bucket_id) THEN
   RAISE EXCEPTION USING ERRCODE='55000',CONSTRAINT='object_multipart_part_capture_fenced',MESSAGE='Capture fences new part transfer admission';
  END IF;
  IF NOT NEW.managed OR NEW.dispatched OR NEW.settled OR u.state<>'active' OR g.transfer_token IS DISTINCT FROM NEW.transfer_token THEN
   RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_multipart_part_writer_conflict',MESSAGE='Reserve part evidence only with new unfenced transfer authority';
  END IF;
 ELSIF (to_jsonb(NEW)-ARRAY['dispatched','settled','copy_intent','put_intent','body_sha256']) IS DISTINCT FROM (to_jsonb(OLD)-ARRAY['dispatched','settled','copy_intent','put_intent','body_sha256']) OR OLD.settled OR NOT OLD.managed OR NOT (
  (OLD.copy_intent IS NULL AND OLD.put_intent IS NULL AND OLD.body_sha256='' AND NEW.body_sha256='' AND NOT OLD.dispatched AND NEW.dispatched AND NOT NEW.settled AND u.state='active' AND u.expires_at>clock_timestamp() AND g.transfer_token IS NOT DISTINCT FROM NEW.transfer_token AND g.unsafe_until>clock_timestamp()) OR
  (NEW.copy_intent IS NOT DISTINCT FROM OLD.copy_intent AND NEW.put_intent IS NOT DISTINCT FROM OLD.put_intent AND NEW.body_sha256=OLD.body_sha256 AND NEW.dispatched=OLD.dispatched AND NEW.settled AND (NOT OLD.dispatched OR g.transfer_token IS NOT DISTINCT FROM NEW.transfer_token))
  OR (OLD.dispatched AND NOT OLD.settled AND NEW.dispatched AND NOT NEW.settled AND OLD.put_intent IS NOT NULL AND OLD.copy_intent IS NULL
   AND NEW.put_intent IS NOT DISTINCT FROM OLD.put_intent AND NEW.copy_intent IS NOT DISTINCT FROM OLD.copy_intent
   AND (OLD.body_sha256='' OR OLD.body_sha256=NEW.body_sha256) AND NEW.body_sha256 ~ '^[0-9a-f]{64}$' AND g.transfer_token IS NOT DISTINCT FROM NEW.transfer_token
   AND (OLD.put_intent->>'expected_sha256'='' OR OLD.put_intent->>'expected_sha256'=NEW.body_sha256))
 ) THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_multipart_part_writer_conflict',MESSAGE='Dispatch once; settle only the original attempt with qualified proof';
 END IF;
 IF TG_OP='UPDATE' AND NOT OLD.dispatched AND NEW.dispatched AND NEW.copy_intent IS NOT NULL AND NOT COALESCE((
  NEW.copy_intent->>'destination_key'=u.object_key AND NEW.copy_intent->>'provider_upload_id'=u.provider_upload_id
  AND (NEW.copy_intent->>'expected_size')::bigint BETWEEN 1 AND g.max_bytes
  AND (NEW.copy_intent->>'source_size')::bigint >= (NEW.copy_intent->>'expected_size')::bigint
  AND NEW.copy_intent->>'source_etag'<>'' AND NEW.copy_intent->>'source_key'<>''
  AND ((g.source_bucket_id IS NULL AND NEW.copy_intent->>'source_bucket_id'=u.bucket_id::text)
   OR (NEW.copy_intent->>'source_bucket_id'=g.source_bucket_id::text AND NEW.copy_intent->>'source_key'=g.source_key))
  AND EXISTS(SELECT 1 FROM object_buckets b WHERE b.id::text=NEW.copy_intent->>'source_bucket_id' AND b.account_id=u.account_id AND b.state='ready'
   AND b.backend_id=NEW.copy_intent->>'source_backend_id' AND b.backend_fingerprint=NEW.copy_intent->>'source_backend_fingerprint' AND b.physical_name=NEW.copy_intent->>'source_physical_name')
  AND (((NEW.copy_intent->>'has_range')::boolean AND (NEW.copy_intent->>'range_first')::bigint>=0 AND (NEW.copy_intent->>'range_last')::bigint<(NEW.copy_intent->>'source_size')::bigint
    AND (NEW.copy_intent->>'expected_size')::bigint=(NEW.copy_intent->>'range_last')::bigint-(NEW.copy_intent->>'range_first')::bigint+1)
   OR (NOT (NEW.copy_intent->>'has_range')::boolean AND (NEW.copy_intent->>'range_first')::bigint=0 AND (NEW.copy_intent->>'range_last')::bigint=0 AND NEW.copy_intent->>'expected_size'=NEW.copy_intent->>'source_size'))
 ),false) THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_multipart_part_writer_conflict',MESSAGE='Copy intent requires the original measured source, range and destination';
 END IF;
 IF TG_OP='UPDATE' AND NOT OLD.dispatched AND NEW.dispatched AND NEW.put_intent IS NOT NULL AND NOT COALESCE((
  NEW.copy_intent IS NULL AND g.source_bucket_id IS NULL
  AND NEW.put_intent->>'destination_key'=u.object_key AND NEW.put_intent->>'provider_upload_id'=u.provider_upload_id
  AND ((g.url_credential_id IS NULL AND (NEW.put_intent->>'expected_size')::bigint BETWEEN 1 AND g.max_bytes)
   OR (g.url_credential_id IS NOT NULL AND u.part_count>0 AND u.part_size_bytes>0 AND u.size_bytes>0
    AND NEW.part_number BETWEEN 1 AND u.part_count
    AND (NEW.put_intent->>'expected_size')::bigint=least(u.part_size_bytes,u.size_bytes-(NEW.part_number-1)::bigint*u.part_size_bytes)))
  AND (NEW.put_intent->>'expected_sha256'='' OR NEW.put_intent->>'expected_sha256' ~ '^[0-9a-f]{64}$')
 ),false) THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_multipart_part_writer_conflict',MESSAGE='PUT intent requires its original destination and admitted size';
 END IF;
 RETURN NEW;
END $_$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.protect_object_version_protection() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE b object_buckets; owner uuid;
BEGIN
 -- The admission/cascade boundary uses account-before-bucket locking.
 SELECT account_id INTO owner FROM object_buckets WHERE id=coalesce(NEW.bucket_id,OLD.bucket_id);
 PERFORM 1 FROM resource_namespaces WHERE id=owner FOR NO KEY UPDATE;
 SELECT * INTO b FROM object_buckets WHERE id=coalesce(NEW.bucket_id,OLD.bucket_id) FOR NO KEY UPDATE;
 IF TG_OP='DELETE' THEN
  IF FOUND AND b.state<>'deleted' THEN RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Preserve protection receipts until physical bucket deletion'; END IF;
  RETURN OLD;
 END IF;
 IF NOT FOUND OR b.state<>'ready' OR (NEW.account_id,NEW.app_id) IS DISTINCT FROM (b.account_id,b.app_id) THEN
  RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Protection requires an owned ready bucket';
 END IF;
 IF TG_OP='INSERT' THEN
  IF NEW.state<>'waiting' OR NEW.dispatched OR NOT object_lock_versioning_ready(NEW.bucket_id) OR NOT object_lock_drained(NEW.bucket_id)
   OR NOT EXISTS(SELECT 1 FROM object_bucket_object_lock WHERE bucket_id=NEW.bucket_id AND state='ready' AND observed_known AND native_enabled_observed AND observed_snapshot->>'enabled'='true')
   OR EXISTS(SELECT 1 FROM object_deletions WHERE bucket_id=NEW.bucket_id AND state IN ('prepared','dispatched'))
   OR EXISTS(SELECT 1 FROM object_bucket_encryption WHERE bucket_id=NEW.bucket_id AND state<>'ready') THEN
   RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Protection requires verified Object Lock and drained mutations';
  END IF;
  IF NEW.public_version_id<>'null' AND NOT EXISTS(SELECT 1 FROM object_version_references WHERE id=NEW.public_version_id::uuid AND bucket_id=NEW.bucket_id AND object_key=NEW.object_key AND native_version_id=NEW.native_version_id) THEN
   RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Protection requires an owned exact version';
  END IF;
 ELSE
  IF (NEW.id,NEW.bucket_id,NEW.account_id,NEW.app_id,NEW.object_key,NEW.public_version_id,NEW.native_version_id,NEW.intent,NEW.created_at)
   IS DISTINCT FROM (OLD.id,OLD.bucket_id,OLD.account_id,OLD.app_id,OLD.object_key,OLD.public_version_id,OLD.native_version_id,OLD.intent,OLD.created_at)
   OR OLD.state IN ('ready','failed') OR OLD.dispatched AND NOT NEW.dispatched
   OR NEW.state='applying' AND NEW.lease_token<>OLD.lease_token AND (OLD.lease_until>clock_timestamp() OR OLD.retry_at>clock_timestamp())
   OR NOT OLD.dispatched AND NEW.dispatched AND NOT (OLD.state='applying' AND NEW.state='applying' AND OLD.lease_token=NEW.lease_token AND OLD.lease_until>clock_timestamp())
   OR NEW.state IN ('ready','failed') AND NOT (OLD.state='applying' AND OLD.lease_until>clock_timestamp())
   OR NEW.state='failed' AND OLD.dispatched AND NEW.last_error_code<>'provider_rejected' THEN
   RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Protection intent and leased progress are immutable';
  END IF;
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.protect_object_write_protection() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE j object_bucket_object_lock; protected boolean; url_request jsonb;
BEGIN
 IF TG_OP='INSERT' THEN
  PERFORM 1 FROM resource_namespaces WHERE id=NEW.account_id FOR NO KEY UPDATE;
  PERFORM 1 FROM object_buckets WHERE id=NEW.bucket_id FOR NO KEY UPDATE;
  SELECT * INTO j FROM object_bucket_object_lock WHERE bucket_id=NEW.bucket_id;
  protected := FOUND AND (j.enabled_required OR j.native_enabled_observed OR j.observed_snapshot->>'enabled'='true');
  IF TG_TABLE_NAME='object_upload_completions' THEN
   IF NEW.status='rejected' AND NEW.protection_snapshot='{}' THEN RETURN NEW; END IF;
   SELECT c.url_request INTO url_request FROM object_storage_s3_credentials c WHERE c.id::text=NEW.subject_id AND c.url_request IS NOT NULL;
   IF FOUND AND coalesce(url_request->'protection','{}'::jsonb) IS DISTINCT FROM coalesce(NEW.protection_snapshot->'requested','{}'::jsonb) THEN RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Signed URL protection requires its bound receipt'; END IF;
  END IF;
  IF protected OR NEW.protection_snapshot<>'{}' THEN
   IF NOT protected OR j.state<>'ready' OR NOT j.observed_known OR j.observed_snapshot->>'enabled' IS DISTINCT FROM 'true' OR
    NOT object_lock_versioning_ready(NEW.bucket_id) OR NEW.protection_snapshot='{}' OR
    coalesce((NEW.protection_snapshot->>'revision')::bigint,0)<>j.revision OR
    coalesce(NEW.protection_snapshot->'default_retention','null'::jsonb) IS DISTINCT FROM coalesce(j.observed_snapshot->'default_retention','null'::jsonb) THEN
    RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='New writes require the admitted Object Lock policy';
   END IF;
   IF TG_TABLE_NAME='object_upload_completions' THEN
    IF NEW.status<>'pending' OR NEW.write_phase<>'prepared' OR NEW.protection_dispatched OR NEW.protection_verified THEN RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Protected write requires a prepared intent'; END IF;
   ELSE
    IF NEW.state<>'initiating' OR NEW.lease_token IS NOT NULL OR NEW.protection_lease_token<>'' OR NEW.protection_verified THEN RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Protected multipart requires an initiating intent'; END IF;
   END IF;
  END IF;
  RETURN NEW;
 END IF;
 IF NEW.protection_snapshot IS DISTINCT FROM OLD.protection_snapshot OR (OLD.protection_snapshot<>'{}' AND
  (NEW.id,NEW.account_id,NEW.app_id,NEW.bucket_id,NEW.object_key,NEW.created_at) IS DISTINCT FROM (OLD.id,OLD.account_id,OLD.app_id,OLD.bucket_id,OLD.object_key,OLD.created_at)) THEN
  RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Write protection snapshot and ownership are immutable';
 END IF;
 IF TG_TABLE_NAME='object_upload_completions' THEN
  IF OLD.protection_snapshot<>'{}' AND ((NEW.bytes,NEW.origin,NEW.source_key,NEW.source_etag) IS DISTINCT FROM (OLD.bytes,OLD.origin,OLD.source_key,OLD.source_etag) OR
   OLD.protection_dispatched AND NOT NEW.protection_dispatched OR NEW.write_phase='dispatched' AND NOT NEW.protection_dispatched OR
   NOT OLD.protection_dispatched AND NEW.protection_dispatched AND NOT (OLD.write_phase='prepared' AND NEW.write_phase='dispatched') OR
   NEW.protection_verified IS DISTINCT FROM OLD.protection_verified AND NOT (OLD.write_phase='dispatched' AND NEW.write_phase='settled' AND NEW.status='completed' AND NEW.protection_verified) OR
   OLD.write_phase='settled' AND (NEW.status,NEW.etag,NEW.error_code,NEW.version_id) IS DISTINCT FROM (OLD.status,OLD.etag,OLD.error_code,OLD.version_id)) THEN
   RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Protected write requires aware dispatch and verified settlement';
  END IF;
 ELSE
  IF OLD.protection_snapshot<>'{}' AND (NEW.lease_token IS NOT NULL AND NEW.protection_lease_token IS DISTINCT FROM NEW.lease_token OR
   NEW.protection_verified IS DISTINCT FROM OLD.protection_verified AND NOT (OLD.state IN ('completing','completing_conditional') AND NEW.state='completed' AND OLD.completion_dispatched AND NEW.protection_verified)) THEN
   RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Protected multipart requires aware claims and verified completion';
  END IF;
  IF NEW.lease_token IS NULL THEN NEW.protection_lease_token:=''; END IF;
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.fence_object_bucket_account_cleanup() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE current_status text; owner_org uuid; adopted boolean;
BEGIN
 SELECT o.id,NOT o.personal_org AND o.resource_ownership_enabled INTO owner_org,adopted
 FROM apps a JOIN orgs o ON o.id=a.org_id WHERE a.id=NEW.app_id AND a.account_id=NEW.account_id;
 IF adopted THEN
  SELECT CASE WHEN deleted_pending THEN 'deleted_pending' ELSE status END INTO current_status FROM orgs WHERE id=owner_org FOR UPDATE;
 ELSE
  SELECT status INTO current_status FROM accounts WHERE id=NEW.account_id FOR UPDATE;
 END IF;
 IF current_status IS DISTINCT FROM 'active' THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_bucket_account_cleanup_fenced',MESSAGE='Inactive storage owner cannot reserve new object buckets';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.guard_bound_multipart_mutation() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE u object_storage_multipart_uploads%ROWTYPE;
BEGIN
 IF TG_OP='UPDATE' THEN
  IF OLD.multipart_upload_id IS NOT NULL OR NEW.multipart_upload_id IS NOT NULL THEN
   RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_multipart_mutation_immutable',MESSAGE='Original multipart receipts are immutable';
  END IF;
  RETURN NEW;
 END IF;
 IF TG_OP='DELETE' THEN
  IF OLD.multipart_upload_id IS NOT NULL AND EXISTS(SELECT 1 FROM object_storage_multipart_uploads WHERE id=OLD.multipart_upload_id AND state NOT IN ('completed','aborted')) THEN
   RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_multipart_mutation_unsettled',MESSAGE='Settle the original session before removing its provider receipt';
  END IF;
  RETURN OLD;
 END IF;
 IF NEW.multipart_upload_id IS NOT NULL THEN
  IF current_setting('transaction_isolation')<>'read committed' THEN
   RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_multipart_mutation_isolation',MESSAGE='Multipart receipt binding requires READ COMMITTED';
  END IF;
  SELECT * INTO u FROM object_storage_multipart_uploads WHERE id=NEW.multipart_upload_id;
  PERFORM id FROM accounts WHERE id=u.account_id FOR UPDATE;
  PERFORM id FROM object_buckets WHERE id=u.bucket_id FOR UPDATE;
  SELECT * INTO u FROM object_storage_multipart_uploads WHERE id=NEW.multipart_upload_id FOR UPDATE;
  IF EXISTS(SELECT 1 FROM object_bucket_write_fences WHERE bucket_id=u.bucket_id) THEN
   RAISE EXCEPTION USING ERRCODE='55000',CONSTRAINT='object_multipart_capture_fenced',MESSAGE='Capture fences new receipt binding';
  END IF;
  IF u.id IS NULL OR u.bucket_id<>NEW.bucket_id OR u.state<>'initiating' OR u.provider_upload_id<>'' OR NEW.kind<>'request' OR NOT EXISTS(SELECT 1 FROM object_buckets WHERE id=u.bucket_id AND account_id=u.account_id AND app_id=u.app_id AND backend_id=NEW.backend_id AND backend_fingerprint=NEW.backend_fingerprint AND physical_name=NEW.physical_name AND state='ready') THEN
   RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_multipart_mutation_original',MESSAGE='Bind only the original reserved session placement';
  END IF;
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.guard_bound_upload_mutation() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
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
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.fence_object_lifecycle_deletion() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE aid uuid; allowed boolean;
BEGIN
 IF TG_OP='UPDATE' AND (NEW.lifecycle_scan_id IS DISTINCT FROM OLD.lifecycle_scan_id OR NEW.lifecycle_binding<>OLD.lifecycle_binding) THEN
  RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Lifecycle deletion identity is immutable';
 END IF;
 IF NEW.lifecycle_scan_id IS NULL THEN RETURN NEW; END IF;
 IF TG_OP='UPDATE' AND NOT (OLD.state='prepared' AND NEW.state='dispatched') THEN RETURN NEW; END IF;
 -- Both the API store and this defensive trigger follow bucket-before-account
 -- lock order. Re-read the lease and revision after acquiring those locks.
 SELECT account_id INTO aid FROM object_buckets WHERE id=NEW.bucket_id AND state='ready' FOR NO KEY UPDATE;
 IF aid IS NULL THEN RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Lifecycle deletion requires a ready owned bucket'; END IF;
 PERFORM 1 FROM resource_namespaces WHERE id=aid FOR UPDATE;
 SELECT EXISTS(
  SELECT 1 FROM object_lifecycle_scans s JOIN object_bucket_lifecycle p ON p.bucket_id=s.bucket_id,
   LATERAL jsonb_array_elements(s.rules) r
  WHERE s.id=NEW.lifecycle_scan_id AND s.bucket_id=NEW.bucket_id AND s.revision=p.revision AND s.state='scanning'
   AND s.lease_token=NEW.lifecycle_binding->>'scan_token' AND s.lease_until>clock_timestamp()
   AND r->>'id'=NEW.lifecycle_binding->>'rule_id' AND r->>'status'='Enabled'
   AND left(NEW.object_key,char_length(coalesce(r->'filter'->>'prefix','')))=coalesce(r->'filter'->>'prefix','')
   AND ((NEW.lifecycle_binding->>'kind'='current' AND (r->'expiration' ? 'days' OR r->'expiration' ? 'date'))
    OR (NEW.lifecycle_binding->>'kind'='noncurrent' AND r ? 'noncurrent_version_expiration')
    OR (NEW.lifecycle_binding->>'kind'='expired_marker' AND (r->'expiration' ? 'days' OR r->'expiration' ? 'date' OR r->'expiration'->>'expired_object_delete_marker'='true')))
 ) INTO allowed;
 IF NOT allowed OR (NEW.lifecycle_binding->>'expected_last_modified')::timestamptz>clock_timestamp()
  OR (NEW.selector='null' AND NEW.lifecycle_binding->>'expected_provider_version_id'<>'null')
  OR (NEW.selector NOT IN ('','null') AND NEW.lifecycle_binding->>'expected_provider_version_id'<>coalesce(to_jsonb(NEW)->>'target_provider_version_id','')) THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_lifecycle_deletion_fenced',MESSAGE='A live matching lifecycle scan and target are required before dispatch';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.fence_object_lifecycle_multipart() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE aid uuid; allowed boolean;
BEGIN
 IF TG_OP='INSERT' THEN
  IF NEW.lifecycle_scan_id IS NOT NULL OR NEW.lifecycle_binding<>'{}' THEN
   RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Multipart lifecycle admission requires an existing active upload';
  END IF;
  RETURN NEW;
 END IF;
 IF OLD.lifecycle_scan_id IS NOT NULL THEN
  IF NEW.lifecycle_scan_id IS DISTINCT FROM OLD.lifecycle_scan_id OR NEW.lifecycle_binding<>OLD.lifecycle_binding
   OR NEW.id<>OLD.id OR NEW.account_id<>OLD.account_id OR NEW.app_id<>OLD.app_id OR NEW.bucket_id<>OLD.bucket_id
   OR NEW.object_key<>OLD.object_key OR NEW.provider_upload_id<>OLD.provider_upload_id OR NEW.created_at<>OLD.created_at THEN
   RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Multipart lifecycle admission identity is immutable';
  END IF;
  RETURN NEW;
 END IF;
 IF NEW.lifecycle_scan_id IS NULL THEN RETURN NEW; END IF;
 IF OLD.state<>'active' OR NEW.state<>'aborting' OR NEW.lease_until IS NOT NULL
  OR OLD.lease_until>clock_timestamp() OR NEW.provider_upload_id<>OLD.provider_upload_id OR NEW.created_at<>OLD.created_at
  OR NEW.id<>OLD.id OR NEW.account_id<>OLD.account_id OR NEW.app_id<>OLD.app_id OR NEW.bucket_id<>OLD.bucket_id OR NEW.object_key<>OLD.object_key
  OR (NEW.lifecycle_binding->>'expected_created_at')::timestamptz<>OLD.created_at THEN
  RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Multipart lifecycle admission requires the unchanged active upload';
 END IF;
 -- The store takes these locks before the upload row. Direct SQL callers must
 -- follow the same bucket/account/upload ordering to avoid deadlocks.
 SELECT account_id INTO aid FROM object_buckets WHERE id=NEW.bucket_id AND account_id=NEW.account_id AND app_id=NEW.app_id AND state='ready' FOR NO KEY UPDATE;
 IF aid IS NULL THEN RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Multipart lifecycle admission requires an owned ready bucket'; END IF;
 PERFORM 1 FROM resource_namespaces WHERE id=aid FOR UPDATE;
 SELECT EXISTS(
  SELECT 1 FROM object_lifecycle_scans s JOIN object_bucket_lifecycle p ON p.bucket_id=s.bucket_id,
   LATERAL jsonb_array_elements(s.rules) r
  WHERE s.id=NEW.lifecycle_scan_id AND s.bucket_id=NEW.bucket_id AND s.revision=p.revision AND s.state='scanning' AND s.phase='multipart'
   AND s.created_at>=NEW.created_at AND (s.last_upload_id IS NULL OR NEW.id>s.last_upload_id)
   AND s.lease_token=NEW.lifecycle_binding->>'scan_token' AND s.lease_until>clock_timestamp()
   AND r->>'id'=NEW.lifecycle_binding->>'rule_id' AND r->>'status'='Enabled'
   AND left(NEW.object_key,char_length(coalesce(r->'filter'->>'prefix','')))=coalesce(r->'filter'->>'prefix','')
   AND coalesce(r->'filter'->'tags','{}')='{}' AND (r->>'abort_incomplete_multipart_days')::bigint>0
   -- Compare elapsed UTC days instead of constructing an out-of-range date
   -- for an otherwise valid large days value.
   AND (clock_timestamp() AT TIME ZONE 'UTC')::date-(NEW.created_at AT TIME ZONE 'UTC')::date > (r->>'abort_incomplete_multipart_days')::bigint
 ) INTO allowed;
 IF NOT allowed THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_lifecycle_multipart_fenced',MESSAGE='A live matching lifecycle scan and aged upload are required before admission';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.fence_object_lifecycle_protection() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE aid uuid; required boolean;
BEGIN
 IF TG_OP='INSERT' THEN
  IF NEW.protection_verified OR NEW.deletion_verified OR NEW.protection_required AND NEW.state<>'prepared' THEN
   RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Protected lifecycle intents must begin prepared and unverified';
  END IF;
  SELECT account_id INTO aid FROM object_buckets WHERE id=NEW.bucket_id;
  PERFORM 1 FROM resource_namespaces WHERE id=aid FOR UPDATE;
  PERFORM 1 FROM object_buckets WHERE id=NEW.bucket_id FOR NO KEY UPDATE;
  SELECT NEW.lifecycle_scan_id IS NOT NULL AND NEW.selector<>'' AND EXISTS(
   SELECT 1 FROM object_bucket_object_lock l WHERE l.bucket_id=NEW.bucket_id
    AND (l.enabled_required OR l.native_enabled_observed OR l.observed_snapshot->>'enabled'='true')
  ) INTO required;
  IF NEW.protection_required IS DISTINCT FROM required OR required AND jsonb_typeof(NEW.lifecycle_binding->'expected_delete_marker') IS DISTINCT FROM 'boolean' THEN
   RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Protected lifecycle deletion requires a frozen target classification';
  END IF;
 ELSE
  IF NEW.protection_required<>OLD.protection_required OR OLD.protection_verified AND NOT NEW.protection_verified OR OLD.deletion_verified AND NOT NEW.deletion_verified THEN
   RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Lifecycle protection evidence is immutable';
  END IF;
  IF NOT OLD.protection_verified AND NEW.protection_verified AND NOT (NEW.protection_required AND OLD.state='prepared' AND NEW.state='dispatched') THEN
   RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Protection proof must accompany initial dispatch';
  END IF;
  IF NOT OLD.deletion_verified AND NEW.deletion_verified AND NOT (OLD.state='dispatched' AND NEW.state='completed') THEN
   RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Absence proof must accompany completion';
  END IF;
  IF NEW.last_error_code='object_protected' AND NOT (OLD.state='prepared' AND NEW.state='failed') AND OLD.last_error_code<>'object_protected' THEN
   RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='A dispatched uncertain attempt cannot be dismissed as protected';
  END IF;
  IF OLD.state='prepared' AND NEW.state='dispatched' AND NEW.protection_required AND NOT NEW.protection_verified THEN
   RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Fresh native policy proof is required before lifecycle dispatch';
  END IF;
 END IF;
 IF NEW.protection_required AND NEW.state='completed' AND (
  NOT NEW.deletion_verified OR jsonb_typeof(NEW.lifecycle_binding->'expected_delete_marker') IS DISTINCT FROM 'boolean'
  OR NEW.delete_marker::text IS DISTINCT FROM NEW.lifecycle_binding->>'expected_delete_marker'
  OR NEW.provider_version_id IS DISTINCT FROM NEW.lifecycle_binding->>'expected_provider_version_id') THEN
  RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Exact lifecycle target absence proof is required before completion';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.fence_object_upload_capture_admission() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE bucket_state text;
BEGIN
 -- Fence queries need a fresh snapshot after waiting for the source lock.
 -- Reject old transaction snapshots rather than hiding a committed hold.
 IF current_setting('transaction_isolation') <> 'read committed' THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_upload_admission_isolation',MESSAGE='Upload admission requires READ COMMITTED';
 END IF;
 -- Match the application admission order: account before source bucket.
 -- An older replica's INSERT must not invert the account FK/row locks.
 PERFORM id FROM resource_namespaces WHERE id=NEW.account_id FOR UPDATE;
 SELECT state INTO bucket_state FROM object_buckets
 WHERE id=NEW.bucket_id AND account_id=NEW.account_id AND app_id=NEW.app_id FOR UPDATE;
 IF bucket_state IS DISTINCT FROM 'ready' THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_upload_bucket_not_ready',MESSAGE='Bucket cleanup fences upload admission';
 END IF;
 IF EXISTS(SELECT 1 FROM object_bucket_write_fences WHERE bucket_id=NEW.bucket_id) THEN
  RAISE EXCEPTION USING ERRCODE='55000',CONSTRAINT='object_upload_capture_fenced',MESSAGE='Checkpoint capture fences new upload admission';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.fence_object_write_key() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE owner uuid; own_write uuid;
BEGIN
 SELECT account_id INTO owner FROM object_buckets WHERE id=NEW.bucket_id;
 PERFORM 1 FROM resource_namespaces WHERE id=owner FOR UPDATE;
 PERFORM 1 FROM object_buckets WHERE id=NEW.bucket_id FOR SHARE;
 IF TG_TABLE_NAME='object_storage_write_admissions' THEN
  own_write:=NEW.id;
 ELSE
  own_write:=NEW.last_write_id;
  IF TG_OP='UPDATE' AND NEW.last_write_id IS NOT DISTINCT FROM OLD.last_write_id THEN own_write:=NULL; END IF;
 END IF;
 IF EXISTS(SELECT 1 FROM object_storage_write_admissions w
  WHERE w.bucket_id=NEW.bucket_id AND w.key_hash=NEW.key_hash AND w.state='pending'
  AND w.id IS DISTINCT FROM own_write
  AND (w.multipart_upload_id IS NULL OR EXISTS(SELECT 1 FROM object_storage_multipart_uploads m
   WHERE m.id=w.multipart_upload_id AND m.state IN ('initiating','active','completing','completing_conditional','aborting')))) THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_write_key_fenced',MESSAGE='Settle the pending write before replacing its proof';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.fence_untracked_object_lock_write() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
 IF TG_TABLE_NAME='object_storage_key_grants' THEN
  IF NEW.last_write_id IS NOT NULL THEN RETURN NEW; END IF;
 ELSE
  IF NEW.kind<>'proxy' OR NEW.route_receipt THEN RETURN NEW; END IF;
 END IF;
 PERFORM 1 FROM resource_namespaces a JOIN object_buckets b ON b.account_id=a.id WHERE b.id=NEW.bucket_id FOR UPDATE OF a;
 PERFORM 1 FROM object_buckets WHERE id=NEW.bucket_id FOR NO KEY UPDATE;
 IF EXISTS(SELECT 1 FROM object_bucket_object_lock WHERE bucket_id=NEW.bucket_id AND (enabled_required OR native_enabled_observed OR state<>'ready')) THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_lock_admission_fenced',MESSAGE='Object Lock requires a tracked protection-aware write';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.guard_object_multipart_initiation_dispatch() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE u object_storage_multipart_uploads%ROWTYPE;
BEGIN
 IF TG_OP='DELETE' THEN
  IF EXISTS(SELECT 1 FROM object_bucket_mutations WHERE multipart_upload_id=OLD.multipart_upload_id) THEN
   RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_multipart_initiation_live',MESSAGE='Retain original initiation evidence while the provider receipt is outstanding';
  END IF;
  RETURN OLD;
 END IF;
 IF current_setting('transaction_isolation')<>'read committed' THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_multipart_initiation_isolation',MESSAGE='Initiation dispatch requires READ COMMITTED';
 END IF;
 SELECT * INTO u FROM object_storage_multipart_uploads WHERE id=NEW.multipart_upload_id;
 PERFORM id FROM resource_namespaces WHERE id=u.account_id FOR UPDATE;
 PERFORM id FROM object_buckets WHERE id=u.bucket_id FOR UPDATE;
 SELECT * INTO u FROM object_storage_multipart_uploads WHERE id=NEW.multipart_upload_id FOR UPDATE;
 IF u.id IS NULL OR u.state<>'initiating' OR u.provider_upload_id<>'' OR NOT EXISTS(
  SELECT 1 FROM object_bucket_mutations m JOIN object_buckets b ON b.id=m.bucket_id
  WHERE m.multipart_upload_id=u.id AND m.kind='request' AND b.id=u.bucket_id AND b.account_id=u.account_id AND b.app_id=u.app_id
  AND b.state='ready' AND b.backend_id=m.backend_id AND b.backend_fingerprint=m.backend_fingerprint AND b.physical_name=m.physical_name
 ) THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_multipart_initiation_original',MESSAGE='Initiation requires the original session receipt and placement';
 END IF;
 IF TG_OP='INSERT' THEN
  IF NEW.dispatched OR NEW.dispatch_token<>'' OR NEW.provider_upload_id<>'' OR u.lease_token IS NOT NULL THEN
   RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_multipart_initiation_reserved',MESSAGE='Initialize dispatch evidence only with a new reservation';
  END IF;
 ELSIF NEW.multipart_upload_id<>OLD.multipart_upload_id OR u.lease_token IS NULL OR u.lease_token='' OR u.lease_until IS NULL OR u.lease_until<=clock_timestamp() OR NOT (
  (NOT OLD.dispatched AND NEW.dispatched AND NEW.dispatch_token=u.lease_token AND NEW.provider_upload_id='') OR
  (OLD.dispatched AND NEW.dispatched AND OLD.dispatch_token=u.lease_token AND NEW.dispatch_token=OLD.dispatch_token AND OLD.provider_upload_id='' AND btrim(NEW.provider_upload_id)<>'' AND NEW.provider_upload_id !~ '[[:cntrl:]]')
 ) THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_multipart_initiation_immutable',MESSAGE='Dispatch is once-only and only its live owner may record a positive reply';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.guard_object_multipart_part_writer() RETURNS trigger
    LANGUAGE plpgsql
    AS $_$
DECLARE u object_storage_multipart_uploads%ROWTYPE; g object_storage_multipart_part_grants%ROWTYPE;
BEGIN
 IF TG_OP='DELETE' THEN
  IF OLD.dispatched AND NOT OLD.settled THEN
   RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_multipart_part_writer_conflict',MESSAGE='Uncertain part evidence cannot be deleted';
  END IF;
  RETURN OLD;
 END IF;
 IF current_setting('transaction_isolation')<>'read committed' THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_multipart_part_writer_conflict',MESSAGE='Part writers require READ COMMITTED';
 END IF;
 SELECT * INTO u FROM object_storage_multipart_uploads WHERE id=NEW.upload_id;
 PERFORM id FROM resource_namespaces WHERE id=u.account_id FOR UPDATE;
 PERFORM id FROM object_buckets WHERE id=u.bucket_id FOR UPDATE;
 SELECT * INTO u FROM object_storage_multipart_uploads WHERE id=NEW.upload_id FOR UPDATE;
 SELECT * INTO g FROM object_storage_multipart_part_grants WHERE upload_id=NEW.upload_id AND part_number=NEW.part_number;
 IF u.id IS NULL OR u.bucket_id<>NEW.bucket_id OR NOT EXISTS(SELECT 1 FROM object_buckets b WHERE b.id=u.bucket_id AND b.account_id=u.account_id AND b.app_id=u.app_id AND b.state='ready' AND b.backend_id=NEW.backend_id AND b.backend_fingerprint=NEW.backend_fingerprint AND b.physical_name=NEW.physical_name) THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_multipart_part_writer_conflict',MESSAGE='Part writer requires its original placement';
 END IF;
 IF TG_OP='INSERT' THEN
  IF EXISTS(SELECT 1 FROM object_bucket_write_fences WHERE bucket_id=u.bucket_id) THEN
   RAISE EXCEPTION USING ERRCODE='55000',CONSTRAINT='object_multipart_part_capture_fenced',MESSAGE='Capture fences new part transfer admission';
  END IF;
  IF NOT NEW.managed OR NEW.dispatched OR NEW.settled OR u.state<>'active' OR g.transfer_token IS DISTINCT FROM NEW.transfer_token THEN
   RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_multipart_part_writer_conflict',MESSAGE='Reserve part evidence only with new unfenced transfer authority';
  END IF;
 ELSIF (to_jsonb(NEW)-ARRAY['dispatched','settled','copy_intent','put_intent','body_sha256']) IS DISTINCT FROM (to_jsonb(OLD)-ARRAY['dispatched','settled','copy_intent','put_intent','body_sha256']) OR OLD.settled OR NOT OLD.managed OR NOT (
  (OLD.copy_intent IS NULL AND OLD.put_intent IS NULL AND OLD.body_sha256='' AND NEW.body_sha256='' AND NOT OLD.dispatched AND NEW.dispatched AND NOT NEW.settled AND u.state='active' AND u.expires_at>clock_timestamp() AND g.transfer_token IS NOT DISTINCT FROM NEW.transfer_token AND g.unsafe_until>clock_timestamp()) OR
  (NEW.copy_intent IS NOT DISTINCT FROM OLD.copy_intent AND NEW.put_intent IS NOT DISTINCT FROM OLD.put_intent AND NEW.body_sha256=OLD.body_sha256 AND NEW.dispatched=OLD.dispatched AND NEW.settled AND (NOT OLD.dispatched OR g.transfer_token IS NOT DISTINCT FROM NEW.transfer_token))
  OR (OLD.dispatched AND NOT OLD.settled AND NEW.dispatched AND NOT NEW.settled AND OLD.put_intent IS NOT NULL AND OLD.copy_intent IS NULL
   AND NEW.put_intent IS NOT DISTINCT FROM OLD.put_intent AND NEW.copy_intent IS NOT DISTINCT FROM OLD.copy_intent
   AND (OLD.body_sha256='' OR OLD.body_sha256=NEW.body_sha256) AND NEW.body_sha256 ~ '^[0-9a-f]{64}$' AND g.transfer_token IS NOT DISTINCT FROM NEW.transfer_token
   AND (OLD.put_intent->>'expected_sha256'='' OR OLD.put_intent->>'expected_sha256'=NEW.body_sha256))
 ) THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_multipart_part_writer_conflict',MESSAGE='Dispatch once; settle only the original attempt with qualified proof';
 END IF;
 IF TG_OP='UPDATE' AND NOT OLD.dispatched AND NEW.dispatched AND NEW.copy_intent IS NOT NULL AND NOT COALESCE((
  NEW.copy_intent->>'destination_key'=u.object_key AND NEW.copy_intent->>'provider_upload_id'=u.provider_upload_id
  AND (NEW.copy_intent->>'expected_size')::bigint BETWEEN 1 AND g.max_bytes
  AND (NEW.copy_intent->>'source_size')::bigint >= (NEW.copy_intent->>'expected_size')::bigint
  AND NEW.copy_intent->>'source_etag'<>'' AND NEW.copy_intent->>'source_key'<>''
  AND ((g.source_bucket_id IS NULL AND NEW.copy_intent->>'source_bucket_id'=u.bucket_id::text)
   OR (NEW.copy_intent->>'source_bucket_id'=g.source_bucket_id::text AND NEW.copy_intent->>'source_key'=g.source_key))
  AND EXISTS(SELECT 1 FROM object_buckets b WHERE b.id::text=NEW.copy_intent->>'source_bucket_id' AND b.account_id=u.account_id AND b.state='ready'
   AND b.backend_id=NEW.copy_intent->>'source_backend_id' AND b.backend_fingerprint=NEW.copy_intent->>'source_backend_fingerprint' AND b.physical_name=NEW.copy_intent->>'source_physical_name')
  AND (((NEW.copy_intent->>'has_range')::boolean AND (NEW.copy_intent->>'range_first')::bigint>=0 AND (NEW.copy_intent->>'range_last')::bigint<(NEW.copy_intent->>'source_size')::bigint
    AND (NEW.copy_intent->>'expected_size')::bigint=(NEW.copy_intent->>'range_last')::bigint-(NEW.copy_intent->>'range_first')::bigint+1)
   OR (NOT (NEW.copy_intent->>'has_range')::boolean AND (NEW.copy_intent->>'range_first')::bigint=0 AND (NEW.copy_intent->>'range_last')::bigint=0 AND NEW.copy_intent->>'expected_size'=NEW.copy_intent->>'source_size'))
 ),false) THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_multipart_part_writer_conflict',MESSAGE='Copy intent requires the original measured source, range and destination';
 END IF;
 IF TG_OP='UPDATE' AND NOT OLD.dispatched AND NEW.dispatched AND NEW.put_intent IS NOT NULL AND NOT COALESCE((
  NEW.copy_intent IS NULL AND g.source_bucket_id IS NULL
  AND NEW.put_intent->>'destination_key'=u.object_key AND NEW.put_intent->>'provider_upload_id'=u.provider_upload_id
  AND ((g.url_credential_id IS NULL AND (NEW.put_intent->>'expected_size')::bigint BETWEEN 1 AND g.max_bytes)
   OR (g.url_credential_id IS NOT NULL AND u.part_count>0 AND u.part_size_bytes>0 AND u.size_bytes>0
    AND NEW.part_number BETWEEN 1 AND u.part_count
    AND (NEW.put_intent->>'expected_size')::bigint=least(u.part_size_bytes,u.size_bytes-(NEW.part_number-1)::bigint*u.part_size_bytes)))
  AND (NEW.put_intent->>'expected_sha256'='' OR NEW.put_intent->>'expected_sha256' ~ '^[0-9a-f]{64}$')
 ),false) THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_multipart_part_writer_conflict',MESSAGE='PUT intent requires its original destination and admitted size';
 END IF;
 RETURN NEW;
END $_$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.protect_object_version_protection() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE b object_buckets; owner uuid;
BEGIN
 -- The admission/cascade boundary uses account-before-bucket locking.
 SELECT account_id INTO owner FROM object_buckets WHERE id=coalesce(NEW.bucket_id,OLD.bucket_id);
 PERFORM 1 FROM resource_namespaces WHERE id=owner FOR UPDATE;
 SELECT * INTO b FROM object_buckets WHERE id=coalesce(NEW.bucket_id,OLD.bucket_id) FOR NO KEY UPDATE;
 IF TG_OP='DELETE' THEN
  IF FOUND AND b.state<>'deleted' THEN RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Preserve protection receipts until physical bucket deletion'; END IF;
  RETURN OLD;
 END IF;
 IF NOT FOUND OR b.state<>'ready' OR (NEW.account_id,NEW.app_id) IS DISTINCT FROM (b.account_id,b.app_id) THEN
  RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Protection requires an owned ready bucket';
 END IF;
 IF TG_OP='INSERT' THEN
  IF NEW.state<>'waiting' OR NEW.dispatched OR NOT object_lock_versioning_ready(NEW.bucket_id) OR NOT object_lock_drained(NEW.bucket_id)
   OR NOT EXISTS(SELECT 1 FROM object_bucket_object_lock WHERE bucket_id=NEW.bucket_id AND state='ready' AND observed_known AND native_enabled_observed AND observed_snapshot->>'enabled'='true')
   OR EXISTS(SELECT 1 FROM object_deletions WHERE bucket_id=NEW.bucket_id AND state IN ('prepared','dispatched'))
   OR EXISTS(SELECT 1 FROM object_bucket_encryption WHERE bucket_id=NEW.bucket_id AND state<>'ready') THEN
   RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Protection requires verified Object Lock and drained mutations';
  END IF;
  IF NEW.public_version_id<>'null' AND NOT EXISTS(SELECT 1 FROM object_version_references WHERE id=NEW.public_version_id::uuid AND bucket_id=NEW.bucket_id AND object_key=NEW.object_key AND native_version_id=NEW.native_version_id) THEN
   RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Protection requires an owned exact version';
  END IF;
 ELSE
  IF (NEW.id,NEW.bucket_id,NEW.account_id,NEW.app_id,NEW.object_key,NEW.public_version_id,NEW.native_version_id,NEW.intent,NEW.created_at)
   IS DISTINCT FROM (OLD.id,OLD.bucket_id,OLD.account_id,OLD.app_id,OLD.object_key,OLD.public_version_id,OLD.native_version_id,OLD.intent,OLD.created_at)
   OR OLD.state IN ('ready','failed') OR OLD.dispatched AND NOT NEW.dispatched
   OR NEW.state='applying' AND NEW.lease_token<>OLD.lease_token AND (OLD.lease_until>clock_timestamp() OR OLD.retry_at>clock_timestamp())
   OR NOT OLD.dispatched AND NEW.dispatched AND NOT (OLD.state='applying' AND NEW.state='applying' AND OLD.lease_token=NEW.lease_token AND OLD.lease_until>clock_timestamp())
   OR NEW.state IN ('ready','failed') AND NOT (OLD.state='applying' AND OLD.lease_until>clock_timestamp())
   OR NEW.state='failed' AND OLD.dispatched AND NEW.last_error_code<>'provider_rejected' THEN
   RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Protection intent and leased progress are immutable';
  END IF;
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.protect_object_write_protection() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE j object_bucket_object_lock; protected boolean; url_request jsonb;
BEGIN
 IF TG_OP='INSERT' THEN
  PERFORM 1 FROM resource_namespaces WHERE id=NEW.account_id FOR UPDATE;
  PERFORM 1 FROM object_buckets WHERE id=NEW.bucket_id FOR NO KEY UPDATE;
  SELECT * INTO j FROM object_bucket_object_lock WHERE bucket_id=NEW.bucket_id;
  protected := FOUND AND (j.enabled_required OR j.native_enabled_observed OR j.observed_snapshot->>'enabled'='true');
  IF TG_TABLE_NAME='object_upload_completions' THEN
   IF NEW.status='rejected' AND NEW.protection_snapshot='{}' THEN RETURN NEW; END IF;
   SELECT c.url_request INTO url_request FROM object_storage_s3_credentials c WHERE c.id::text=NEW.subject_id AND c.url_request IS NOT NULL;
   IF FOUND AND coalesce(url_request->'protection','{}'::jsonb) IS DISTINCT FROM coalesce(NEW.protection_snapshot->'requested','{}'::jsonb) THEN RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Signed URL protection requires its bound receipt'; END IF;
  END IF;
  IF protected OR NEW.protection_snapshot<>'{}' THEN
   IF NOT protected OR j.state<>'ready' OR NOT j.observed_known OR j.observed_snapshot->>'enabled' IS DISTINCT FROM 'true' OR
    NOT object_lock_versioning_ready(NEW.bucket_id) OR NEW.protection_snapshot='{}' OR
    coalesce((NEW.protection_snapshot->>'revision')::bigint,0)<>j.revision OR
    coalesce(NEW.protection_snapshot->'default_retention','null'::jsonb) IS DISTINCT FROM coalesce(j.observed_snapshot->'default_retention','null'::jsonb) THEN
    RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='New writes require the admitted Object Lock policy';
   END IF;
   IF TG_TABLE_NAME='object_upload_completions' THEN
    IF NEW.status<>'pending' OR NEW.write_phase<>'prepared' OR NEW.protection_dispatched OR NEW.protection_verified THEN RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Protected write requires a prepared intent'; END IF;
   ELSE
    IF NEW.state<>'initiating' OR NEW.lease_token IS NOT NULL OR NEW.protection_lease_token<>'' OR NEW.protection_verified THEN RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Protected multipart requires an initiating intent'; END IF;
   END IF;
  END IF;
  RETURN NEW;
 END IF;
 IF NEW.protection_snapshot IS DISTINCT FROM OLD.protection_snapshot OR (OLD.protection_snapshot<>'{}' AND
  (NEW.id,NEW.account_id,NEW.app_id,NEW.bucket_id,NEW.object_key,NEW.created_at) IS DISTINCT FROM (OLD.id,OLD.account_id,OLD.app_id,OLD.bucket_id,OLD.object_key,OLD.created_at)) THEN
  RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Write protection snapshot and ownership are immutable';
 END IF;
 IF TG_TABLE_NAME='object_upload_completions' THEN
  IF OLD.protection_snapshot<>'{}' AND ((NEW.bytes,NEW.origin,NEW.source_key,NEW.source_etag) IS DISTINCT FROM (OLD.bytes,OLD.origin,OLD.source_key,OLD.source_etag) OR
   OLD.protection_dispatched AND NOT NEW.protection_dispatched OR NEW.write_phase='dispatched' AND NOT NEW.protection_dispatched OR
   NOT OLD.protection_dispatched AND NEW.protection_dispatched AND NOT (OLD.write_phase='prepared' AND NEW.write_phase='dispatched') OR
   NEW.protection_verified IS DISTINCT FROM OLD.protection_verified AND NOT (OLD.write_phase='dispatched' AND NEW.write_phase='settled' AND NEW.status='completed' AND NEW.protection_verified) OR
   OLD.write_phase='settled' AND (NEW.status,NEW.etag,NEW.error_code,NEW.version_id) IS DISTINCT FROM (OLD.status,OLD.etag,OLD.error_code,OLD.version_id)) THEN
   RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Protected write requires aware dispatch and verified settlement';
  END IF;
 ELSE
  IF OLD.protection_snapshot<>'{}' AND (NEW.lease_token IS NOT NULL AND NEW.protection_lease_token IS DISTINCT FROM NEW.lease_token OR
   NEW.protection_verified IS DISTINCT FROM OLD.protection_verified AND NOT (OLD.state IN ('completing','completing_conditional') AND NEW.state='completed' AND OLD.completion_dispatched AND NEW.protection_verified)) THEN
   RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Protected multipart requires aware claims and verified completion';
  END IF;
  IF NEW.lease_token IS NULL THEN NEW.protection_lease_token:=''; END IF;
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
