-- migration-compat: contract
-- Organization object ownership and human credential revocation (ADR-202610171907), applied online (ADR-202610171900, ADR-202610171908).
-- Replaces the single-transaction 20261018003100006, which never ran in production.
--
-- Each step is its own short transaction on at most one existing table (plus
-- the table a new foreign key references), waits at most 500ms for a lock
-- (below deadlock_timeout, so the migration is the side that yields) and
-- retries. Backfills commit every batch. Constraints are added NOT VALID and
-- validated separately. Every step is idempotent.
-- migration-lint: allow not-null object_buckets.org_id: bind_object_bucket_organization derives organization ownership from the existing app_id for legacy INSERTs after the validated backfill.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '500ms';

-- ---------------------------------------------------------------- 1. new nullable columns
-- +goose StatementBegin
-- orgs: object_storage_owned_from column
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   ALTER TABLE orgs ADD COLUMN IF NOT EXISTS object_storage_owned_from timestamptz;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: orgs: object_storage_owned_from column' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- object_buckets: org_id column
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   ALTER TABLE object_buckets ADD COLUMN IF NOT EXISTS org_id uuid;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: object_buckets: org_id column' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- object_storage_s3_credentials: issuer columns
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   ALTER TABLE object_storage_s3_credentials ADD COLUMN IF NOT EXISTS issuer_account_id uuid,
       ADD COLUMN IF NOT EXISTS issuer_access_revision bigint;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: object_storage_s3_credentials: issuer columns' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd

-- ---------------------------------------------------------------- 2. functions that do not depend on the backfill
-- object_storage_key_live
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION object_storage_key_live(key_id uuid,bucket_id uuid) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT EXISTS(SELECT 1 FROM api_keys k JOIN object_buckets b ON b.id=bucket_id JOIN orgs o ON o.id=b.org_id
 WHERE k.id=key_id AND k.status IN ('active','grace') AND (k.expires_at IS NULL OR k.expires_at>now())

 AND ((o.personal_org AND k.account_id=b.account_id AND k.org_id=o.id)
 OR (NOT o.personal_org AND k.org_id=o.id AND EXISTS(SELECT 1 FROM org_memberships m
 WHERE m.org_id=o.id AND m.account_id=k.account_id AND m.removed_at IS NULL AND m.role IN ('owner','admin','developer')))))
$$;
-- +goose StatementEnd
-- fence_object_bucket_account_cleanup
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION fence_object_bucket_account_cleanup() RETURNS trigger LANGUAGE plpgsql AS $$
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
-- object lifecycle fences lock durable namespaces
-- +goose StatementBegin
-- Preserve existing proof, writer and protection fences on durable namespaces.
DO $$ DECLARE signature text; declaration text;
BEGIN
 FOREACH signature IN ARRAY ARRAY['fence_object_lifecycle_deletion()','fence_object_lifecycle_multipart()','fence_object_lifecycle_protection()','fence_object_upload_capture_admission()','fence_object_write_key()','fence_untracked_object_lock_write()','guard_object_multipart_initiation_dispatch()','guard_object_multipart_part_writer()','protect_object_s3_copy_source_epoch()','protect_object_version_protection()','protect_object_write_protection()'] LOOP
  SELECT pg_get_functiondef(to_regprocedure(signature)) INTO declaration;
  EXECUTE replace(declaration,'FROM accounts ','FROM resource_namespaces ');
 END LOOP;
END $$;
-- +goose StatementEnd
-- object_storage_usage_subject
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION object_storage_usage_subject(owner_org uuid,namespace uuid,period_start timestamptz) RETURNS uuid LANGUAGE sql STABLE AS $$
 SELECT CASE WHEN EXISTS(SELECT 1 FROM orgs o WHERE o.id=owner_org AND NOT o.personal_org AND o.resource_ownership_enabled
 AND period_start>=date_trunc('month',coalesce(o.object_storage_owned_from,now()) AT TIME ZONE 'UTC') AT TIME ZONE 'UTC') THEN owner_org ELSE namespace END
$$;
-- +goose StatementEnd
-- object_storage_usage_subject
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION object_storage_usage_subject(owner_org uuid,namespace uuid) RETURNS uuid LANGUAGE sql STABLE AS $$
 SELECT object_storage_usage_subject(owner_org,namespace,date_trunc('month',now()))
$$;
-- +goose StatementEnd
-- bind_object_bucket_organization
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION bind_object_bucket_organization() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE parent_org uuid; parent_account uuid;
BEGIN
 IF TG_OP='UPDATE' AND (NEW.org_id IS DISTINCT FROM OLD.org_id OR NEW.account_id<>OLD.account_id OR NEW.app_id<>OLD.app_id) THEN
  RAISE EXCEPTION 'bucket ownership is immutable' USING ERRCODE='23514',CONSTRAINT='object_bucket_organization';
 END IF;
 SELECT a.org_id,a.account_id INTO parent_org,parent_account FROM apps a WHERE a.id=NEW.app_id;
 IF NEW.org_id IS NULL THEN NEW.org_id=parent_org; END IF;
 IF parent_org IS NULL OR NEW.org_id<>parent_org OR NEW.account_id<>parent_account THEN
  RAISE EXCEPTION 'invalid bucket organization' USING ERRCODE='23514',CONSTRAINT='object_bucket_organization';
 END IF;
 PERFORM id FROM orgs WHERE id=NEW.org_id AND NOT deleted_pending FOR SHARE;
 IF NOT FOUND THEN RAISE EXCEPTION 'bucket organization unavailable' USING ERRCODE='23514',CONSTRAINT='object_bucket_organization'; END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
-- object_storage_credential_live
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION object_storage_credential_live(credential uuid) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT EXISTS(SELECT 1 FROM object_storage_s3_credentials c JOIN object_buckets b ON b.id=c.bucket_id
 JOIN orgs o ON o.id=b.org_id WHERE c.id=credential AND c.status='active' AND b.state='ready' AND NOT o.deleted_pending
 AND (CASE WHEN NOT o.personal_org AND o.resource_ownership_enabled THEN o.status IN ('active','past_due') AND o.abuse_hold_at IS NULL
 ELSE EXISTS(SELECT 1 FROM accounts a WHERE a.id=b.account_id AND a.status IN ('active','past_due') AND a.abuse_hold_at IS NULL) END)
 AND (o.personal_org OR c.managed_app_id IS NOT NULL OR c.rotation_parent_id IS NOT NULL
      OR EXISTS(SELECT 1 FROM org_memberships m WHERE m.org_id=b.org_id AND m.account_id=c.issuer_account_id
         AND m.removed_at IS NULL AND m.access_revision=c.issuer_access_revision AND m.role IN ('owner','admin','developer'))))
$$;
-- +goose StatementEnd
-- stamp_object_storage_issuer
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION stamp_object_storage_issuer() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE owner_org uuid; personal boolean; issuer_revision bigint;
BEGIN
 SELECT b.org_id,o.personal_org INTO owner_org,personal FROM object_buckets b JOIN orgs o ON o.id=b.org_id
 WHERE b.id=NEW.bucket_id AND b.account_id=NEW.account_id;
 IF NOT FOUND THEN RAISE EXCEPTION 'invalid storage credential bucket' USING ERRCODE='23514'; END IF;
 IF NEW.managed_app_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM apps a WHERE a.id=NEW.managed_app_id AND a.org_id=owner_org AND a.account_id=NEW.account_id) THEN
  RAISE EXCEPTION 'invalid storage binding organization' USING ERRCODE='23514';
 END IF;
 IF TG_OP='UPDATE' AND (NEW.account_id<>OLD.account_id OR NEW.bucket_id<>OLD.bucket_id OR NEW.issuer_account_id IS DISTINCT FROM OLD.issuer_account_id
 OR NEW.issuer_access_revision IS DISTINCT FROM OLD.issuer_access_revision OR NEW.managed_app_id IS DISTINCT FROM OLD.managed_app_id OR NEW.rotation_parent_id IS DISTINCT FROM OLD.rotation_parent_id) THEN
  RAISE EXCEPTION 'storage credential ownership is immutable' USING ERRCODE='23514';
 END IF;
 IF (NEW.managed_app_id IS NOT NULL OR NEW.rotation_parent_id IS NOT NULL) AND NEW.issuer_account_id IS NOT NULL THEN
  RAISE EXCEPTION 'application credentials cannot carry human authority' USING ERRCODE='23514';
 END IF;
 IF NOT personal AND NEW.managed_app_id IS NULL AND NEW.rotation_parent_id IS NULL THEN
  NEW.issuer_account_id=coalesce(NEW.issuer_account_id,(SELECT account_id FROM api_keys WHERE id=NEW.url_api_key_id),NEW.account_id);
  SELECT access_revision INTO issuer_revision FROM org_memberships WHERE org_id=owner_org AND account_id=NEW.issuer_account_id
   AND removed_at IS NULL AND role IN ('owner','admin','developer') FOR SHARE;
  IF NOT FOUND OR (NEW.issuer_access_revision IS NOT NULL AND NEW.issuer_access_revision<>issuer_revision) THEN
   RAISE EXCEPTION 'organization access revoked' USING ERRCODE='42501';
  END IF;
  NEW.issuer_access_revision=issuer_revision;
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
-- revoke_object_storage_member_keys
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION revoke_object_storage_member_keys() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' OR NEW.access_revision<>OLD.access_revision THEN
  UPDATE object_storage_s3_credentials c SET status='revoked',revoked_at=coalesce(c.revoked_at,now())
  FROM object_buckets b WHERE b.id=c.bucket_id AND b.org_id=OLD.org_id AND c.issuer_account_id=OLD.account_id AND c.status='active';
 END IF;
 RETURN NULL;
END $$;
-- +goose StatementEnd
-- guard_org_object_buckets
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_org_object_buckets() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF (NEW.deleted_pending OR NEW.status='deleted_pending') AND EXISTS(SELECT 1 FROM object_buckets b WHERE b.org_id=NEW.id AND b.state<>'deleted') THEN
  RAISE EXCEPTION 'organization has object buckets' USING ERRCODE='23514',CONSTRAINT='org_has_object_buckets';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
-- guard_object_storage_billing_origin
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_object_storage_billing_origin() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF OLD.object_storage_owned_from IS NOT NULL AND NEW.object_storage_owned_from IS DISTINCT FROM OLD.object_storage_owned_from THEN
  RAISE EXCEPTION 'storage billing origin is immutable' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
-- invalidate_object_usage_on_adoption
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION invalidate_object_usage_on_adoption() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.resource_ownership_enabled AND (TG_OP='INSERT' OR NOT OLD.resource_ownership_enabled) THEN
  IF NEW.object_storage_owned_from IS NULL THEN
   UPDATE orgs SET object_storage_owned_from=now() WHERE id=NEW.id;
  END IF;
  IF EXISTS(SELECT 1 FROM object_storage_billing_periods p WHERE p.period_start>=date_trunc('month',now())
    AND EXISTS(SELECT 1 FROM object_buckets b WHERE b.org_id=NEW.id AND b.account_id=p.account_id)) THEN
   RAISE EXCEPTION 'remediate finalized current-period object billing before adoption' USING ERRCODE='23514';
  END IF;
  DELETE FROM object_storage_usage_heads h WHERE h.period_start>=date_trunc('month',now()) AND
   (h.account_id=NEW.id OR EXISTS(SELECT 1 FROM object_buckets b WHERE b.org_id=NEW.id AND b.account_id=h.account_id));
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd

-- ---------------------------------------------------------------- 3. triggers that derive the new fields for legacy writers
-- +goose StatementBegin
-- orgs: trigger object_storage_billing_origin
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   CREATE OR REPLACE TRIGGER object_storage_billing_origin BEFORE UPDATE OF object_storage_owned_from ON orgs
    FOR EACH ROW EXECUTE FUNCTION guard_object_storage_billing_origin();
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: orgs: trigger object_storage_billing_origin' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- orgs: trigger object_usage_adoption
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   CREATE OR REPLACE TRIGGER object_usage_adoption AFTER INSERT OR UPDATE OF resource_ownership_enabled ON orgs
    FOR EACH ROW EXECUTE FUNCTION invalidate_object_usage_on_adoption();
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: orgs: trigger object_usage_adoption' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- object_buckets: trigger object_bucket_organization
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   CREATE OR REPLACE TRIGGER object_bucket_organization BEFORE INSERT OR UPDATE OF org_id,account_id,app_id ON object_buckets
    FOR EACH ROW EXECUTE FUNCTION bind_object_bucket_organization();
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: object_buckets: trigger object_bucket_organization' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd

-- ---------------------------------------------------------------- 4. backfills in batches
-- +goose StatementBegin
-- orgs: storage billing origin for adopted organizations
DO $online$
DECLARE last uuid := '00000000-0000-0000-0000-000000000000'; ids uuid[]; failures integer := 0;
BEGIN
 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   SELECT array_agg(id ORDER BY id) INTO ids FROM (SELECT id FROM orgs WHERE NOT personal_org AND resource_ownership_enabled AND object_storage_owned_from IS NULL AND id > last ORDER BY id LIMIT 100) batch;
   IF ids IS NOT NULL THEN
    UPDATE orgs SET object_storage_owned_from = now() WHERE id = ANY (ids) AND object_storage_owned_from IS NULL;
    last := ids[cardinality(ids)];
   END IF;
   failures := 0;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   failures := failures + 1;
   IF failures > 600 THEN RAISE; END IF;
   ids := ARRAY[]::uuid[];
   PERFORM pg_sleep(0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN ids IS NULL;
 END LOOP;
END
$online$;
-- +goose StatementEnd
-- object_buckets: refuse inconsistent ownership before changing anything
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM object_buckets b JOIN apps a ON a.id=b.app_id WHERE b.account_id<>a.account_id) THEN
  RAISE EXCEPTION 'remediate inconsistent object bucket ownership' USING ERRCODE='23514';
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
-- object_buckets: org_id derived from the app by the ownership trigger
DO $online$
DECLARE last uuid := '00000000-0000-0000-0000-000000000000'; ids uuid[]; failures integer := 0;
BEGIN
 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   SELECT array_agg(id ORDER BY id) INTO ids FROM (SELECT id FROM object_buckets WHERE org_id IS NULL AND id > last ORDER BY id LIMIT 500) batch;
   IF ids IS NOT NULL THEN
    UPDATE object_buckets SET org_id = NULL WHERE id = ANY (ids) AND org_id IS NULL;
    last := ids[cardinality(ids)];
   END IF;
   failures := 0;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   failures := failures + 1;
   IF failures > 600 THEN RAISE; END IF;
   ids := ARRAY[]::uuid[];
   PERFORM pg_sleep(0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN ids IS NULL;
 END LOOP;
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- object_storage_s3_credentials: human issuer and membership revision, revoked when no longer a member
DO $online$
DECLARE last uuid := '00000000-0000-0000-0000-000000000000'; ids uuid[]; failures integer := 0;
BEGIN
 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   SELECT array_agg(id ORDER BY id) INTO ids FROM (SELECT c.id FROM object_storage_s3_credentials c JOIN object_buckets b ON b.id=c.bucket_id JOIN orgs o ON o.id=b.org_id WHERE NOT o.personal_org AND c.managed_app_id IS NULL AND c.rotation_parent_id IS NULL AND c.issuer_account_id IS NULL AND c.id > last ORDER BY c.id LIMIT 500) batch;
   IF ids IS NOT NULL THEN
    UPDATE object_storage_s3_credentials c SET issuer_account_id=b.account_id,issuer_access_revision=coalesce(m.access_revision,1)
    FROM object_buckets b JOIN orgs o ON o.id=b.org_id
    LEFT JOIN org_memberships m ON m.org_id=o.id AND m.account_id=b.account_id AND m.removed_at IS NULL
    WHERE c.bucket_id=b.id AND NOT o.personal_org AND c.managed_app_id IS NULL AND c.rotation_parent_id IS NULL
     AND c.issuer_account_id IS NULL AND c.id = ANY (ids);
    UPDATE object_storage_s3_credentials c SET issuer_account_id=k.account_id,issuer_access_revision=coalesce(m.access_revision,1)
    FROM api_keys k LEFT JOIN org_memberships m ON m.org_id=k.org_id AND m.account_id=k.account_id AND m.removed_at IS NULL
    WHERE c.url_api_key_id=k.id AND c.issuer_account_id IS NOT NULL AND c.id = ANY (ids);
    UPDATE object_storage_s3_credentials c SET status='revoked',revoked_at=coalesce(revoked_at,now())
    WHERE issuer_account_id IS NOT NULL AND status <> 'revoked' AND NOT EXISTS(SELECT 1 FROM object_buckets b JOIN org_memberships m ON m.org_id=b.org_id
     WHERE b.id=c.bucket_id AND m.account_id=c.issuer_account_id AND m.removed_at IS NULL
     AND m.access_revision=c.issuer_access_revision AND m.role IN ('owner','admin','developer')) AND c.id = ANY (ids);
    last := ids[cardinality(ids)];
   END IF;
   failures := 0;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   failures := failures + 1;
   IF failures > 600 THEN RAISE; END IF;
   ids := ARRAY[]::uuid[];
   PERFORM pg_sleep(0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN ids IS NULL;
 END LOOP;
END
$online$;
-- +goose StatementEnd
-- object storage: refuse mixed organization bindings
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM object_storage_s3_credentials c JOIN object_buckets b ON b.id=c.bucket_id
 JOIN apps a ON a.id=c.managed_app_id WHERE a.org_id<>b.org_id OR a.account_id<>b.account_id)
 OR EXISTS(SELECT 1 FROM object_s3_copy_source_grants g JOIN object_buckets b ON b.id=g.bucket_id
 JOIN object_buckets source ON source.id=g.source_bucket_id WHERE b.org_id<>source.org_id) THEN
  RAISE EXCEPTION 'remediate mixed organization storage bindings before migration' USING ERRCODE='23514';
 END IF;
END $$;
-- +goose StatementEnd

-- ---------------------------------------------------------------- 5. triggers and functions that read the backfilled columns
-- +goose StatementBegin
-- object_storage_s3_credentials: catch up and install the issuer trigger under one short write lock
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   LOCK TABLE object_storage_s3_credentials IN SHARE ROW EXCLUSIVE MODE;
   CREATE TEMP TABLE org_issuer_catch_up ON COMMIT DROP AS
    SELECT c.id FROM object_storage_s3_credentials c JOIN object_buckets b ON b.id=c.bucket_id JOIN orgs o ON o.id=b.org_id
    WHERE NOT o.personal_org AND c.managed_app_id IS NULL AND c.rotation_parent_id IS NULL AND c.issuer_account_id IS NULL;
   IF (SELECT count(*) FROM org_issuer_catch_up) > 10000 THEN
    RAISE EXCEPTION 'object_storage_s3_credentials: more rows changed during the backfill than one catch-up covers; run the migration again' USING ERRCODE = '55000';
   END IF;
   UPDATE object_storage_s3_credentials c SET issuer_account_id=b.account_id,issuer_access_revision=coalesce(m.access_revision,1)
       FROM object_buckets b JOIN orgs o ON o.id=b.org_id
       LEFT JOIN org_memberships m ON m.org_id=o.id AND m.account_id=b.account_id AND m.removed_at IS NULL
       WHERE c.bucket_id=b.id AND NOT o.personal_org AND c.managed_app_id IS NULL AND c.rotation_parent_id IS NULL
        AND c.issuer_account_id IS NULL AND c.id IN (SELECT x.id FROM object_storage_s3_credentials x WHERE x.id IN (SELECT id FROM org_issuer_catch_up) LIMIT 10000);
       UPDATE object_storage_s3_credentials c SET issuer_account_id=k.account_id,issuer_access_revision=coalesce(m.access_revision,1)
       FROM api_keys k LEFT JOIN org_memberships m ON m.org_id=k.org_id AND m.account_id=k.account_id AND m.removed_at IS NULL
       WHERE c.url_api_key_id=k.id AND c.issuer_account_id IS NOT NULL AND c.id IN (SELECT x.id FROM object_storage_s3_credentials x WHERE x.id IN (SELECT id FROM org_issuer_catch_up) LIMIT 10000);
       UPDATE object_storage_s3_credentials c SET status='revoked',revoked_at=coalesce(revoked_at,now())
       WHERE issuer_account_id IS NOT NULL AND status <> 'revoked' AND NOT EXISTS(SELECT 1 FROM object_buckets b JOIN org_memberships m ON m.org_id=b.org_id
        WHERE b.id=c.bucket_id AND m.account_id=c.issuer_account_id AND m.removed_at IS NULL
        AND m.access_revision=c.issuer_access_revision AND m.role IN ('owner','admin','developer')) AND c.id IN (SELECT x.id FROM object_storage_s3_credentials x WHERE x.id IN (SELECT id FROM org_issuer_catch_up) LIMIT 10000);
   CREATE OR REPLACE TRIGGER object_storage_issuer BEFORE INSERT OR UPDATE OF account_id,bucket_id,issuer_account_id,issuer_access_revision,managed_app_id,rotation_parent_id ON object_storage_s3_credentials
    FOR EACH ROW EXECUTE FUNCTION stamp_object_storage_issuer();
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: object_storage_s3_credentials: catch up and install the issuer trigger under one short write lock' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- object_url_issuer_live
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION object_url_issuer_live(owner uuid, bucket uuid, issuer uuid, permission text, expiry timestamp with time zone) RETURNS boolean
    LANGUAGE sql
    AS $$
 SELECT expiry>clock_timestamp() AND EXISTS(SELECT 1 FROM object_buckets b WHERE b.id=bucket AND b.account_id=owner AND b.state='ready') AND
  (issuer IS NULL OR EXISTS(SELECT 1 FROM api_keys k
    WHERE k.id=issuer AND object_storage_key_live(k.id,bucket) AND k.status IN ('active','grace') AND (k.expires_at IS NULL OR k.expires_at>clock_timestamp()) AND
    ('admin'=ANY(k.scopes) OR (('storage:'||permission)=ANY(k.scopes) AND EXISTS(SELECT 1 FROM object_storage_access_grants g WHERE g.api_key_id=k.id AND g.account_id=owner AND g.bucket_id=bucket AND (g.permission=permission OR g.permission='read_write'))))));
$$;
-- +goose StatementEnd
-- +goose StatementBegin
-- org_memberships: trigger object_storage_member_revocation
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   CREATE OR REPLACE TRIGGER object_storage_member_revocation AFTER UPDATE OR DELETE ON org_memberships
    FOR EACH ROW EXECUTE FUNCTION revoke_object_storage_member_keys();
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: org_memberships: trigger object_storage_member_revocation' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- orgs: trigger org_object_buckets_guard
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   CREATE OR REPLACE TRIGGER org_object_buckets_guard BEFORE UPDATE OF status,deleted_pending ON orgs
    FOR EACH ROW EXECUTE FUNCTION guard_org_object_buckets();
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: orgs: trigger org_object_buckets_guard' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- org_app_identity_erasure_blocked
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION org_app_identity_erasure_blocked(identity_id uuid) RETURNS boolean
    LANGUAGE plpgsql STABLE
    AS $_$
DECLARE resource_table text; found boolean;
BEGIN
    IF EXISTS (SELECT 1 FROM org_memberships m JOIN orgs o ON o.id=m.org_id
               WHERE m.account_id=identity_id AND m.removed_at IS NULL
                 AND m.role='owner' AND NOT o.personal_org AND NOT o.deleted_pending) THEN
        RETURN true;
    END IF;
    IF EXISTS (SELECT 1 FROM managed_postgres_databases d JOIN orgs o ON o.id=d.org_id
               WHERE d.account_id=identity_id AND NOT o.personal_org AND NOT o.resource_ownership_enabled) THEN
        RETURN true;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM managed_postgres_databases d JOIN orgs o ON o.id=d.org_id
                   WHERE d.account_id=identity_id AND NOT o.personal_org)
       AND NOT EXISTS (SELECT 1 FROM apps a JOIN orgs o ON o.id=a.org_id
                   WHERE a.account_id=identity_id AND NOT o.personal_org)
       AND NOT EXISTS (SELECT 1 FROM projects p JOIN orgs o ON o.id=p.org_id
                       WHERE p.account_id=identity_id AND NOT o.personal_org) THEN
        RETURN false;
    END IF;
    IF EXISTS (SELECT 1 FROM apps a JOIN orgs o ON o.id=a.org_id
               WHERE a.account_id=identity_id AND NOT o.personal_org
                 AND (a.github_install_id IS NOT NULL OR NOT o.resource_ownership_enabled)) THEN
        RETURN true;
    END IF;
    IF EXISTS (SELECT 1 FROM projects p JOIN orgs o ON o.id=p.org_id
               WHERE p.account_id=identity_id AND NOT o.personal_org
                 AND (coalesce(p.install_id,0) <> 0 OR NOT o.resource_ownership_enabled))
       OR EXISTS (SELECT 1 FROM environment_git_sources g JOIN projects p ON p.id=g.project_id
                  JOIN orgs o ON o.id=p.org_id WHERE p.account_id=identity_id AND NOT o.personal_org) THEN
        RETURN true;
    END IF;
    FOR resource_table IN SELECT unnest(ARRAY[
        'alert_deliveries',
        'alert_rules',
        'api_consumer_plan_admissions',
        'api_consumer_plan_assignments',
        'api_consumer_plans',
        'api_consumer_rate_cards',
        'api_consumer_route_usage_minutes',
        'api_consumer_usage_alerts',
        'api_consumer_usage_events',
        'api_consumer_usage_minutes',
        'api_consumer_usage_statement_handoffs',
        'api_consumer_usage_statements',
        'api_consumers',
        'app_api_routes',
        'app_issues',
        'app_private_network_attachment_nodes',
        'app_private_network_attachments',
        'app_route_priorities',
        'app_route_removal_policies',
        'app_tcp_listeners',
        'app_udp_listeners',
        'app_webhook_deliveries',
        'app_webhook_event_outbox',
        'app_webhooks',
        'application_standard_snapshot_captures',
        'automatic_route_checks',
        'canary_route_gates',
        'ci_automation_branches',
        'ci_automation_event_retries',
        'ci_automation_events',
        'ci_automation_path_checks',
        'ci_automation_policies',
        'ci_automation_pull_requests',
        'ci_execution_archives',
        'ci_execution_check_retries',
        'ci_execution_checks',
        'ci_execution_profiles',
        'ci_executions',
        'ci_github_bindings',
        'ci_github_checks',
        'ci_github_triggers',
        'ci_job_attempts',
        'ci_managed_objects',
        'ci_pipeline_revisions',
        'ci_run_events',
        'ci_runs',
        'ci_source_check_retries',
        'commit_sources',
        'consumer_keys',
        'cors_presets',
        'customer_operation_definitions',
        'customer_operation_idempotency',
        'customer_operation_stream_leases',
        'customer_operation_webhook_bindings',
        'customer_operation_workflow_states',
        'customer_operations',
        'customer_workflow_playbook_progress',
        'customer_workflow_readiness_subscriptions',
        'data_upstreams',
        'dead_letter_events',
        'dev_bridge_sessions',
        'dev_bridge_webhook_replays',
        'edge_rules',
        'entity_outbox_acceptances',
        'event_recovery_jobs',
        'event_subscription_capacity_commands',
        'event_subscription_circuit_breakers',
        'event_subscription_delivery_controls',
        'event_subscriptions',
        'exclusive_work_operations',
        'exclusive_work_trigger_bindings',
        'inbound_webhook_endpoints',
        'invocation_environment_queue_admissions',
        'issue_ingest_tokens',
        'job_runs',
        'managed_realtime_drain_operations',
        'managed_realtime_endpoints',
        'mcp_catalog_monitors',
        'mcp_readiness_attention_actions',
        'mcp_readiness_incidents',
        'mcp_readiness_maintenance_windows',
        'mcp_readiness_notification_actions',
        'app_slos',
        'notification_channels',
        'synthetic_checks',
        'meterd_tenant_surface_cert_expiry_state',
        'mirror_rules',
        'outbound_app_bindings',
        'platform_tenant_usage_minutes',
        'prewarm_intents',
        'profile_canary_checks',
        'profile_deployment_checks',
        'profile_deployment_policies',
        'profile_investigations',
        'profile_periodic_monitors',
        'profile_route_alert_state',
        'project_environment_clone_checkpoint_postgres_credentials',
        'project_environment_clone_postgres_archives',
        'project_environment_clone_postgres_contents',
        'project_environment_clone_postgres_copy_readers',
        'project_environment_clone_postgres_copy_targets',
        'project_environment_clone_postgres_database_plans',
        'project_environment_clone_postgres_database_sql_pins',
        'project_environment_clone_postgres_imports',
        'project_environment_clone_postgres_inventories',
        'project_environment_clone_postgres_membership_plans',
        'project_environment_clone_postgres_role_plans',
        'project_environment_clone_postgres_snapshot_restores',
        'project_environment_clone_postgres_target_sql_pins',
        'project_environment_clone_postgres_verification_read_budgets',
        'project_environment_clone_postgres_verifications',
        'project_environment_queue_runtime_sets',
        'queue_bindings',
        'reserved_ip_leases',
        'route_check_history',
        'route_health_gates',
        'route_health_history',
        'route_health_notification_state',
        'route_lifecycle_approvals',
        'route_monitor_incidents',
        'route_monitors',
        'route_policy_receipts',
        'route_removal_approvals',
        'runtime_upgrade_operations',
        'saved_route_requirements',
        'product_control_billing_documents',
        'product_control_customer_changes',
        'product_control_customers',
        'product_control_email_subscriptions',
        'product_control_heads',
        'product_control_help_requests',
        'product_control_invoice_handoffs',
        'product_control_notification_reads',
        'product_control_onboardings',
        'product_control_plan_changes',
        'product_control_receipts',
        'product_control_revisions',
        'managed_postgres_connection_capacity',
        'data_api_runtime_connection_observations',
        'data_repair_rehearsals',
        'scenario_cold_preparations',
        'scenario_managed_work',
        'scenario_object_fixtures',
        'scenario_postgres_faults',
        'scenario_postgres_fixtures',
        'scenario_suspend_resumes',
        'scenario_test_action_policies',
        'scenario_test_chaos_matches',
        'scenario_test_members',
        'scenario_worker_faults',
        'schedule_occurrences',
        'tenant_surfaces',
        'triggers',
        'workflow_automation_publish_receipts',
        'workflow_operation_effects',
        'workflow_run_resumes'
    ]::text[]) LOOP
        EXECUTE format('SELECT EXISTS (SELECT 1 FROM %I WHERE account_id = $1)', resource_table)
            INTO found USING identity_id;
        IF found THEN RETURN true; END IF;
    END LOOP;
    RETURN false;
END $_$;
-- +goose StatementEnd
-- object storage: refuse finalized current-period billing for adopted organizations
-- +goose StatementBegin
-- Invalidate mutable mixed aggregates; immutable reports and invoices survive.
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM object_storage_billing_periods p WHERE p.period_start>=date_trunc('month',now())
  AND EXISTS(SELECT 1 FROM object_buckets b JOIN orgs o ON o.id=b.org_id WHERE b.account_id=p.account_id AND NOT o.personal_org AND o.resource_ownership_enabled)) THEN
  RAISE EXCEPTION 'remediate finalized current-period object billing before ownership cutover' USING ERRCODE='23514';
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
-- object_storage_usage_heads: invalidate mutable current-period aggregates of adopted organizations
DO $online$
DECLARE moved bigint; failures integer := 0;
BEGIN
 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   DELETE FROM object_storage_usage_heads WHERE ctid = ANY (ARRAY(SELECT h.ctid FROM object_storage_usage_heads h WHERE h.period_start>=date_trunc('month',now()) AND EXISTS (SELECT 1 FROM object_buckets b JOIN orgs o ON o.id=b.org_id WHERE b.account_id=h.account_id AND NOT o.personal_org AND o.resource_ownership_enabled) LIMIT 500));
   GET DIAGNOSTICS moved = ROW_COUNT;
   failures := 0;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   failures := failures + 1;
   IF failures > 600 THEN RAISE; END IF;
   moved := -1;
   PERFORM pg_sleep(0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN moved = 0;
 END LOOP;
END
$online$;
-- +goose StatementEnd

-- ---------------------------------------------------------------- 6. constraints NOT VALID
-- +goose StatementBegin
-- orgs: add org_object_storage_owned_from_finite NOT VALID
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'orgs'::regclass AND conname = 'org_object_storage_owned_from_finite') THEN
    ALTER TABLE orgs ADD CONSTRAINT org_object_storage_owned_from_finite CHECK (object_storage_owned_from IS NULL OR isfinite(object_storage_owned_from)) NOT VALID;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: orgs: add org_object_storage_owned_from_finite NOT VALID' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- object_buckets: add object_buckets_org_id_fkey NOT VALID
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'object_buckets'::regclass AND conname = 'object_buckets_org_id_fkey') THEN
    ALTER TABLE object_buckets ADD CONSTRAINT object_buckets_org_id_fkey FOREIGN KEY (org_id) REFERENCES orgs(id) NOT VALID;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: object_buckets: add object_buckets_org_id_fkey NOT VALID' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- object_storage_s3_credentials: add object_s3_issuer_pair NOT VALID
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'object_storage_s3_credentials'::regclass AND conname = 'object_s3_issuer_pair') THEN
    ALTER TABLE object_storage_s3_credentials ADD CONSTRAINT object_s3_issuer_pair CHECK ((issuer_account_id IS NULL AND issuer_access_revision IS NULL) OR (issuer_account_id IS NOT NULL AND issuer_access_revision IS NOT NULL AND issuer_access_revision>0)) NOT VALID;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: object_storage_s3_credentials: add object_s3_issuer_pair NOT VALID' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- object_buckets.org_id: prove NOT NULL with a NOT VALID check
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_attribute WHERE attrelid = 'object_buckets'::regclass AND attname = 'org_id' AND NOT attnotnull) AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'object_buckets'::regclass AND conname = 'org_nn_8564a496b978') THEN
    ALTER TABLE object_buckets ADD CONSTRAINT org_nn_8564a496b978 CHECK (org_id IS NOT NULL) NOT VALID;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: object_buckets.org_id: prove NOT NULL with a NOT VALID check' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- event_fanout_outbox: add the namespace foreign key NOT VALID
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'event_fanout_outbox'::regclass AND conname = 'event_fanout_outbox_account_id_fkey' AND confrelid = 'accounts'::regclass)
      AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'event_fanout_outbox'::regclass AND conname = 'org_ns_fk_ec63f0944798') THEN
    ALTER TABLE event_fanout_outbox ADD CONSTRAINT org_ns_fk_ec63f0944798 FOREIGN KEY(account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE NOT VALID;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: event_fanout_outbox: add the namespace foreign key NOT VALID' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- object_buckets: add the namespace foreign key NOT VALID
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'object_buckets'::regclass AND conname = 'object_buckets_account_id_fkey' AND confrelid = 'accounts'::regclass)
      AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'object_buckets'::regclass AND conname = 'org_ns_fk_b648d4edbc87') THEN
    ALTER TABLE object_buckets ADD CONSTRAINT org_ns_fk_b648d4edbc87 FOREIGN KEY(account_id) REFERENCES resource_namespaces(id) NOT VALID;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: object_buckets: add the namespace foreign key NOT VALID' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- object_s3_copy_source_epochs: add the namespace foreign key NOT VALID
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'object_s3_copy_source_epochs'::regclass AND conname = 'object_s3_copy_source_epochs_account_id_fkey' AND confrelid = 'accounts'::regclass)
      AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'object_s3_copy_source_epochs'::regclass AND conname = 'org_ns_fk_3ee00c326709') THEN
    ALTER TABLE object_s3_copy_source_epochs ADD CONSTRAINT org_ns_fk_3ee00c326709 FOREIGN KEY(account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE NOT VALID;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: object_s3_copy_source_epochs: add the namespace foreign key NOT VALID' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- object_s3_copy_source_grants: add the namespace foreign key NOT VALID
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'object_s3_copy_source_grants'::regclass AND conname = 'object_s3_copy_source_grants_account_id_fkey' AND confrelid = 'accounts'::regclass)
      AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'object_s3_copy_source_grants'::regclass AND conname = 'org_ns_fk_79feeb784d0f') THEN
    ALTER TABLE object_s3_copy_source_grants ADD CONSTRAINT org_ns_fk_79feeb784d0f FOREIGN KEY(account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE NOT VALID;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: object_s3_copy_source_grants: add the namespace foreign key NOT VALID' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- object_storage_access_grants: add the namespace foreign key NOT VALID
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'object_storage_access_grants'::regclass AND conname = 'object_storage_access_grants_account_id_fkey' AND confrelid = 'accounts'::regclass)
      AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'object_storage_access_grants'::regclass AND conname = 'org_ns_fk_6003f4aa8a8f') THEN
    ALTER TABLE object_storage_access_grants ADD CONSTRAINT org_ns_fk_6003f4aa8a8f FOREIGN KEY(account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE NOT VALID;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: object_storage_access_grants: add the namespace foreign key NOT VALID' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- object_storage_authorizations: add the namespace foreign key NOT VALID
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'object_storage_authorizations'::regclass AND conname = 'object_storage_authorizations_account_id_fkey' AND confrelid = 'accounts'::regclass)
      AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'object_storage_authorizations'::regclass AND conname = 'org_ns_fk_db7b20f561c5') THEN
    ALTER TABLE object_storage_authorizations ADD CONSTRAINT org_ns_fk_db7b20f561c5 FOREIGN KEY(account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE NOT VALID;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: object_storage_authorizations: add the namespace foreign key NOT VALID' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- object_storage_billing_periods: add the namespace foreign key NOT VALID
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'object_storage_billing_periods'::regclass AND conname = 'object_storage_billing_periods_account_id_fkey' AND confrelid = 'accounts'::regclass)
      AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'object_storage_billing_periods'::regclass AND conname = 'org_ns_fk_02cda69e3685') THEN
    ALTER TABLE object_storage_billing_periods ADD CONSTRAINT org_ns_fk_02cda69e3685 FOREIGN KEY(account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE NOT VALID;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: object_storage_billing_periods: add the namespace foreign key NOT VALID' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- object_storage_customer_usage_reports_v2: add the namespace foreign key NOT VALID
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'object_storage_customer_usage_reports_v2'::regclass AND conname = 'object_storage_customer_usage_reports_v2_account_id_fkey' AND confrelid = 'accounts'::regclass)
      AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'object_storage_customer_usage_reports_v2'::regclass AND conname = 'org_ns_fk_2f14d7c704dc') THEN
    ALTER TABLE object_storage_customer_usage_reports_v2 ADD CONSTRAINT org_ns_fk_2f14d7c704dc FOREIGN KEY(account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE NOT VALID;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: object_storage_customer_usage_reports_v2: add the namespace foreign key NOT VALID' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- object_storage_multipart_uploads: add the namespace foreign key NOT VALID
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'object_storage_multipart_uploads'::regclass AND conname = 'object_storage_multipart_uploads_account_id_fkey' AND confrelid = 'accounts'::regclass)
      AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'object_storage_multipart_uploads'::regclass AND conname = 'org_ns_fk_e0a60f7b1fe0') THEN
    ALTER TABLE object_storage_multipart_uploads ADD CONSTRAINT org_ns_fk_e0a60f7b1fe0 FOREIGN KEY(account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE NOT VALID;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: object_storage_multipart_uploads: add the namespace foreign key NOT VALID' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- object_storage_s3_credentials: add the namespace foreign key NOT VALID
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'object_storage_s3_credentials'::regclass AND conname = 'object_storage_s3_credentials_account_id_fkey' AND confrelid = 'accounts'::regclass)
      AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'object_storage_s3_credentials'::regclass AND conname = 'org_ns_fk_6ee2b4e0bd2c') THEN
    ALTER TABLE object_storage_s3_credentials ADD CONSTRAINT org_ns_fk_6ee2b4e0bd2c FOREIGN KEY(account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE NOT VALID;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: object_storage_s3_credentials: add the namespace foreign key NOT VALID' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- object_storage_upload_grants: add the namespace foreign key NOT VALID
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'object_storage_upload_grants'::regclass AND conname = 'object_storage_upload_grants_account_id_fkey' AND confrelid = 'accounts'::regclass)
      AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'object_storage_upload_grants'::regclass AND conname = 'org_ns_fk_a3948fe3645d') THEN
    ALTER TABLE object_storage_upload_grants ADD CONSTRAINT org_ns_fk_a3948fe3645d FOREIGN KEY(account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE NOT VALID;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: object_storage_upload_grants: add the namespace foreign key NOT VALID' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- object_storage_usage_reports: add the namespace foreign key NOT VALID
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'object_storage_usage_reports'::regclass AND conname = 'object_storage_usage_reports_account_id_fkey' AND confrelid = 'accounts'::regclass)
      AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'object_storage_usage_reports'::regclass AND conname = 'org_ns_fk_e347d2858258') THEN
    ALTER TABLE object_storage_usage_reports ADD CONSTRAINT org_ns_fk_e347d2858258 FOREIGN KEY(account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE NOT VALID;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: object_storage_usage_reports: add the namespace foreign key NOT VALID' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- object_upload_completions: add the namespace foreign key NOT VALID
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'object_upload_completions'::regclass AND conname = 'object_upload_completions_account_id_fkey' AND confrelid = 'accounts'::regclass)
      AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'object_upload_completions'::regclass AND conname = 'org_ns_fk_682f9222af86') THEN
    ALTER TABLE object_upload_completions ADD CONSTRAINT org_ns_fk_682f9222af86 FOREIGN KEY(account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE NOT VALID;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: object_upload_completions: add the namespace foreign key NOT VALID' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- object_upload_routes: add the namespace foreign key NOT VALID
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'object_upload_routes'::regclass AND conname = 'object_upload_routes_account_id_fkey' AND confrelid = 'accounts'::regclass)
      AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'object_upload_routes'::regclass AND conname = 'org_ns_fk_be97fc4a76b2') THEN
    ALTER TABLE object_upload_routes ADD CONSTRAINT org_ns_fk_be97fc4a76b2 FOREIGN KEY(account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE NOT VALID;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: object_upload_routes: add the namespace foreign key NOT VALID' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- object_version_protection: add the namespace foreign key NOT VALID
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'object_version_protection'::regclass AND conname = 'object_version_protection_account_id_fkey' AND confrelid = 'accounts'::regclass)
      AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'object_version_protection'::regclass AND conname = 'org_ns_fk_e671e091b387') THEN
    ALTER TABLE object_version_protection ADD CONSTRAINT org_ns_fk_e671e091b387 FOREIGN KEY(account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE NOT VALID;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: object_version_protection: add the namespace foreign key NOT VALID' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd

-- ---------------------------------------------------------------- 7. validate
-- +goose StatementBegin
-- orgs: validate org_object_storage_owned_from_finite
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '5s', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'orgs'::regclass AND conname = 'org_object_storage_owned_from_finite' AND NOT convalidated) THEN
    ALTER TABLE orgs VALIDATE CONSTRAINT org_object_storage_owned_from_finite;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: orgs: validate org_object_storage_owned_from_finite' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- object_buckets: validate object_buckets_org_id_fkey
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '5s', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'object_buckets'::regclass AND conname = 'object_buckets_org_id_fkey' AND NOT convalidated) THEN
    ALTER TABLE object_buckets VALIDATE CONSTRAINT object_buckets_org_id_fkey;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: object_buckets: validate object_buckets_org_id_fkey' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- object_storage_s3_credentials: validate object_s3_issuer_pair
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '5s', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'object_storage_s3_credentials'::regclass AND conname = 'object_s3_issuer_pair' AND NOT convalidated) THEN
    ALTER TABLE object_storage_s3_credentials VALIDATE CONSTRAINT object_s3_issuer_pair;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: object_storage_s3_credentials: validate object_s3_issuer_pair' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- object_buckets: validate org_nn_8564a496b978
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '5s', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'object_buckets'::regclass AND conname = 'org_nn_8564a496b978' AND NOT convalidated) THEN
    ALTER TABLE object_buckets VALIDATE CONSTRAINT org_nn_8564a496b978;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: object_buckets: validate org_nn_8564a496b978' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- event_fanout_outbox: validate the namespace foreign key
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '5s', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'event_fanout_outbox'::regclass AND conname = 'org_ns_fk_ec63f0944798' AND NOT convalidated) THEN
    ALTER TABLE event_fanout_outbox VALIDATE CONSTRAINT org_ns_fk_ec63f0944798;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: event_fanout_outbox: validate the namespace foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- object_buckets: validate the namespace foreign key
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '5s', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'object_buckets'::regclass AND conname = 'org_ns_fk_b648d4edbc87' AND NOT convalidated) THEN
    ALTER TABLE object_buckets VALIDATE CONSTRAINT org_ns_fk_b648d4edbc87;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: object_buckets: validate the namespace foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- object_s3_copy_source_epochs: validate the namespace foreign key
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '5s', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'object_s3_copy_source_epochs'::regclass AND conname = 'org_ns_fk_3ee00c326709' AND NOT convalidated) THEN
    ALTER TABLE object_s3_copy_source_epochs VALIDATE CONSTRAINT org_ns_fk_3ee00c326709;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: object_s3_copy_source_epochs: validate the namespace foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- object_s3_copy_source_grants: validate the namespace foreign key
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '5s', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'object_s3_copy_source_grants'::regclass AND conname = 'org_ns_fk_79feeb784d0f' AND NOT convalidated) THEN
    ALTER TABLE object_s3_copy_source_grants VALIDATE CONSTRAINT org_ns_fk_79feeb784d0f;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: object_s3_copy_source_grants: validate the namespace foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- object_storage_access_grants: validate the namespace foreign key
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '5s', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'object_storage_access_grants'::regclass AND conname = 'org_ns_fk_6003f4aa8a8f' AND NOT convalidated) THEN
    ALTER TABLE object_storage_access_grants VALIDATE CONSTRAINT org_ns_fk_6003f4aa8a8f;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: object_storage_access_grants: validate the namespace foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- object_storage_authorizations: validate the namespace foreign key
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '5s', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'object_storage_authorizations'::regclass AND conname = 'org_ns_fk_db7b20f561c5' AND NOT convalidated) THEN
    ALTER TABLE object_storage_authorizations VALIDATE CONSTRAINT org_ns_fk_db7b20f561c5;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: object_storage_authorizations: validate the namespace foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- object_storage_billing_periods: validate the namespace foreign key
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '5s', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'object_storage_billing_periods'::regclass AND conname = 'org_ns_fk_02cda69e3685' AND NOT convalidated) THEN
    ALTER TABLE object_storage_billing_periods VALIDATE CONSTRAINT org_ns_fk_02cda69e3685;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: object_storage_billing_periods: validate the namespace foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- object_storage_customer_usage_reports_v2: validate the namespace foreign key
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '5s', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'object_storage_customer_usage_reports_v2'::regclass AND conname = 'org_ns_fk_2f14d7c704dc' AND NOT convalidated) THEN
    ALTER TABLE object_storage_customer_usage_reports_v2 VALIDATE CONSTRAINT org_ns_fk_2f14d7c704dc;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: object_storage_customer_usage_reports_v2: validate the namespace foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- object_storage_multipart_uploads: validate the namespace foreign key
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '5s', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'object_storage_multipart_uploads'::regclass AND conname = 'org_ns_fk_e0a60f7b1fe0' AND NOT convalidated) THEN
    ALTER TABLE object_storage_multipart_uploads VALIDATE CONSTRAINT org_ns_fk_e0a60f7b1fe0;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: object_storage_multipart_uploads: validate the namespace foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- object_storage_s3_credentials: validate the namespace foreign key
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '5s', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'object_storage_s3_credentials'::regclass AND conname = 'org_ns_fk_6ee2b4e0bd2c' AND NOT convalidated) THEN
    ALTER TABLE object_storage_s3_credentials VALIDATE CONSTRAINT org_ns_fk_6ee2b4e0bd2c;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: object_storage_s3_credentials: validate the namespace foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- object_storage_upload_grants: validate the namespace foreign key
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '5s', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'object_storage_upload_grants'::regclass AND conname = 'org_ns_fk_a3948fe3645d' AND NOT convalidated) THEN
    ALTER TABLE object_storage_upload_grants VALIDATE CONSTRAINT org_ns_fk_a3948fe3645d;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: object_storage_upload_grants: validate the namespace foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- object_storage_usage_reports: validate the namespace foreign key
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '5s', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'object_storage_usage_reports'::regclass AND conname = 'org_ns_fk_e347d2858258' AND NOT convalidated) THEN
    ALTER TABLE object_storage_usage_reports VALIDATE CONSTRAINT org_ns_fk_e347d2858258;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: object_storage_usage_reports: validate the namespace foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- object_upload_completions: validate the namespace foreign key
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '5s', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'object_upload_completions'::regclass AND conname = 'org_ns_fk_682f9222af86' AND NOT convalidated) THEN
    ALTER TABLE object_upload_completions VALIDATE CONSTRAINT org_ns_fk_682f9222af86;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: object_upload_completions: validate the namespace foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- object_upload_routes: validate the namespace foreign key
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '5s', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'object_upload_routes'::regclass AND conname = 'org_ns_fk_be97fc4a76b2' AND NOT convalidated) THEN
    ALTER TABLE object_upload_routes VALIDATE CONSTRAINT org_ns_fk_be97fc4a76b2;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: object_upload_routes: validate the namespace foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- object_version_protection: validate the namespace foreign key
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '5s', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'object_version_protection'::regclass AND conname = 'org_ns_fk_e671e091b387' AND NOT convalidated) THEN
    ALTER TABLE object_version_protection VALIDATE CONSTRAINT org_ns_fk_e671e091b387;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: object_version_protection: validate the namespace foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd

-- ---------------------------------------------------------------- 8. retire replaced constraints, tighten, index
-- +goose StatementBegin
-- event_fanout_outbox: retire the accounts foreign key
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'event_fanout_outbox'::regclass AND conname = 'org_ns_fk_ec63f0944798') THEN
    ALTER TABLE event_fanout_outbox DROP CONSTRAINT IF EXISTS event_fanout_outbox_account_id_fkey;
    ALTER TABLE event_fanout_outbox RENAME CONSTRAINT org_ns_fk_ec63f0944798 TO event_fanout_outbox_account_id_fkey;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: event_fanout_outbox: retire the accounts foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- object_buckets: retire the accounts foreign key
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'object_buckets'::regclass AND conname = 'org_ns_fk_b648d4edbc87') THEN
    ALTER TABLE object_buckets DROP CONSTRAINT IF EXISTS object_buckets_account_id_fkey;
    ALTER TABLE object_buckets RENAME CONSTRAINT org_ns_fk_b648d4edbc87 TO object_buckets_account_id_fkey;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: object_buckets: retire the accounts foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- object_s3_copy_source_epochs: retire the accounts foreign key
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'object_s3_copy_source_epochs'::regclass AND conname = 'org_ns_fk_3ee00c326709') THEN
    ALTER TABLE object_s3_copy_source_epochs DROP CONSTRAINT IF EXISTS object_s3_copy_source_epochs_account_id_fkey;
    ALTER TABLE object_s3_copy_source_epochs RENAME CONSTRAINT org_ns_fk_3ee00c326709 TO object_s3_copy_source_epochs_account_id_fkey;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: object_s3_copy_source_epochs: retire the accounts foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- object_s3_copy_source_grants: retire the accounts foreign key
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'object_s3_copy_source_grants'::regclass AND conname = 'org_ns_fk_79feeb784d0f') THEN
    ALTER TABLE object_s3_copy_source_grants DROP CONSTRAINT IF EXISTS object_s3_copy_source_grants_account_id_fkey;
    ALTER TABLE object_s3_copy_source_grants RENAME CONSTRAINT org_ns_fk_79feeb784d0f TO object_s3_copy_source_grants_account_id_fkey;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: object_s3_copy_source_grants: retire the accounts foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- object_storage_access_grants: retire the accounts foreign key
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'object_storage_access_grants'::regclass AND conname = 'org_ns_fk_6003f4aa8a8f') THEN
    ALTER TABLE object_storage_access_grants DROP CONSTRAINT IF EXISTS object_storage_access_grants_account_id_fkey;
    ALTER TABLE object_storage_access_grants RENAME CONSTRAINT org_ns_fk_6003f4aa8a8f TO object_storage_access_grants_account_id_fkey;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: object_storage_access_grants: retire the accounts foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- object_storage_authorizations: retire the accounts foreign key
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'object_storage_authorizations'::regclass AND conname = 'org_ns_fk_db7b20f561c5') THEN
    ALTER TABLE object_storage_authorizations DROP CONSTRAINT IF EXISTS object_storage_authorizations_account_id_fkey;
    ALTER TABLE object_storage_authorizations RENAME CONSTRAINT org_ns_fk_db7b20f561c5 TO object_storage_authorizations_account_id_fkey;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: object_storage_authorizations: retire the accounts foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- object_storage_billing_periods: retire the accounts foreign key
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'object_storage_billing_periods'::regclass AND conname = 'org_ns_fk_02cda69e3685') THEN
    ALTER TABLE object_storage_billing_periods DROP CONSTRAINT IF EXISTS object_storage_billing_periods_account_id_fkey;
    ALTER TABLE object_storage_billing_periods RENAME CONSTRAINT org_ns_fk_02cda69e3685 TO object_storage_billing_periods_account_id_fkey;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: object_storage_billing_periods: retire the accounts foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- object_storage_customer_usage_reports_v2: retire the accounts foreign key
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'object_storage_customer_usage_reports_v2'::regclass AND conname = 'org_ns_fk_2f14d7c704dc') THEN
    ALTER TABLE object_storage_customer_usage_reports_v2 DROP CONSTRAINT IF EXISTS object_storage_customer_usage_reports_v2_account_id_fkey;
    ALTER TABLE object_storage_customer_usage_reports_v2 RENAME CONSTRAINT org_ns_fk_2f14d7c704dc TO object_storage_customer_usage_reports_v2_account_id_fkey;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: object_storage_customer_usage_reports_v2: retire the accounts foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- object_storage_multipart_uploads: retire the accounts foreign key
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'object_storage_multipart_uploads'::regclass AND conname = 'org_ns_fk_e0a60f7b1fe0') THEN
    ALTER TABLE object_storage_multipart_uploads DROP CONSTRAINT IF EXISTS object_storage_multipart_uploads_account_id_fkey;
    ALTER TABLE object_storage_multipart_uploads RENAME CONSTRAINT org_ns_fk_e0a60f7b1fe0 TO object_storage_multipart_uploads_account_id_fkey;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: object_storage_multipart_uploads: retire the accounts foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- object_storage_s3_credentials: retire the accounts foreign key
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'object_storage_s3_credentials'::regclass AND conname = 'org_ns_fk_6ee2b4e0bd2c') THEN
    ALTER TABLE object_storage_s3_credentials DROP CONSTRAINT IF EXISTS object_storage_s3_credentials_account_id_fkey;
    ALTER TABLE object_storage_s3_credentials RENAME CONSTRAINT org_ns_fk_6ee2b4e0bd2c TO object_storage_s3_credentials_account_id_fkey;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: object_storage_s3_credentials: retire the accounts foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- object_storage_upload_grants: retire the accounts foreign key
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'object_storage_upload_grants'::regclass AND conname = 'org_ns_fk_a3948fe3645d') THEN
    ALTER TABLE object_storage_upload_grants DROP CONSTRAINT IF EXISTS object_storage_upload_grants_account_id_fkey;
    ALTER TABLE object_storage_upload_grants RENAME CONSTRAINT org_ns_fk_a3948fe3645d TO object_storage_upload_grants_account_id_fkey;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: object_storage_upload_grants: retire the accounts foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- object_storage_usage_reports: retire the accounts foreign key
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'object_storage_usage_reports'::regclass AND conname = 'org_ns_fk_e347d2858258') THEN
    ALTER TABLE object_storage_usage_reports DROP CONSTRAINT IF EXISTS object_storage_usage_reports_account_id_fkey;
    ALTER TABLE object_storage_usage_reports RENAME CONSTRAINT org_ns_fk_e347d2858258 TO object_storage_usage_reports_account_id_fkey;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: object_storage_usage_reports: retire the accounts foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- object_upload_completions: retire the accounts foreign key
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'object_upload_completions'::regclass AND conname = 'org_ns_fk_682f9222af86') THEN
    ALTER TABLE object_upload_completions DROP CONSTRAINT IF EXISTS object_upload_completions_account_id_fkey;
    ALTER TABLE object_upload_completions RENAME CONSTRAINT org_ns_fk_682f9222af86 TO object_upload_completions_account_id_fkey;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: object_upload_completions: retire the accounts foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- object_upload_routes: retire the accounts foreign key
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'object_upload_routes'::regclass AND conname = 'org_ns_fk_be97fc4a76b2') THEN
    ALTER TABLE object_upload_routes DROP CONSTRAINT IF EXISTS object_upload_routes_account_id_fkey;
    ALTER TABLE object_upload_routes RENAME CONSTRAINT org_ns_fk_be97fc4a76b2 TO object_upload_routes_account_id_fkey;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: object_upload_routes: retire the accounts foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- object_version_protection: retire the accounts foreign key
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'object_version_protection'::regclass AND conname = 'org_ns_fk_e671e091b387') THEN
    ALTER TABLE object_version_protection DROP CONSTRAINT IF EXISTS object_version_protection_account_id_fkey;
    ALTER TABLE object_version_protection RENAME CONSTRAINT org_ns_fk_e671e091b387 TO object_version_protection_account_id_fkey;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: object_version_protection: retire the accounts foreign key' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- object_buckets.org_id: SET NOT NULL from the validated check
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_attribute WHERE attrelid = 'object_buckets'::regclass AND attname = 'org_id' AND NOT attnotnull) THEN
    ALTER TABLE object_buckets ALTER COLUMN org_id SET NOT NULL;
   END IF;
   ALTER TABLE object_buckets DROP CONSTRAINT IF EXISTS org_nn_8564a496b978;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: object_buckets.org_id: SET NOT NULL from the validated check' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- +goose StatementBegin
-- object_buckets_org_idx: drop an interrupted build
DO $online$
DECLARE done boolean := false;
BEGIN
 FOR attempt IN 1..600 LOOP
  BEGIN
   PERFORM set_config('lock_timeout', '500ms', true);
   SET CONSTRAINTS ALL IMMEDIATE;
   IF EXISTS (SELECT 1 FROM pg_index WHERE indexrelid = to_regclass('object_buckets_org_idx') AND NOT indisvalid) THEN
    DROP INDEX object_buckets_org_idx;
   END IF;
   done := true;
  EXCEPTION WHEN lock_not_available OR deadlock_detected THEN
   PERFORM pg_sleep(least(attempt, 20) * 0.05);
  END;
  PERFORM set_config('lock_timeout', '5s', true);
  COMMIT;
  EXIT WHEN done;
 END LOOP;
 IF done THEN RETURN; END IF;
 RAISE EXCEPTION 'online migration step did not get its lock: object_buckets_org_idx: drop an interrupted build' USING ERRCODE = '55P03';
END
$online$;
-- +goose StatementEnd
-- object_buckets_org_idx: build concurrently (waits only for older transactions)
SET lock_timeout = '10s';
CREATE INDEX CONCURRENTLY IF NOT EXISTS object_buckets_org_idx ON object_buckets (org_id,id);
SET lock_timeout = '500ms';
RESET lock_timeout;

-- +goose Down
-- Rollback is an operator action on a drained schema, not part of a release:
-- releases roll back by running the previous binaries against this schema.
SET lock_timeout = '5s';
-- +goose StatementBegin
ALTER TABLE event_fanout_outbox DROP CONSTRAINT event_fanout_outbox_account_id_fkey;
ALTER TABLE event_fanout_outbox ADD CONSTRAINT event_fanout_outbox_account_id_fkey FOREIGN KEY(account_id) REFERENCES accounts(id) ON DELETE CASCADE;
CREATE OR REPLACE FUNCTION object_url_issuer_live(owner uuid, bucket uuid, issuer uuid, permission text, expiry timestamp with time zone) RETURNS boolean
    LANGUAGE sql
    AS $$
 SELECT expiry>clock_timestamp() AND EXISTS(SELECT 1 FROM object_buckets b WHERE b.id=bucket AND b.account_id=owner AND b.state='ready') AND
  (issuer IS NULL OR EXISTS(SELECT 1 FROM api_keys k
    WHERE k.id=issuer AND k.account_id=owner AND k.status IN ('active','grace') AND (k.expires_at IS NULL OR k.expires_at>clock_timestamp()) AND
    ('admin'=ANY(k.scopes) OR (('storage:'||permission)=ANY(k.scopes) AND EXISTS(SELECT 1 FROM object_storage_access_grants g WHERE g.api_key_id=k.id AND g.account_id=owner AND g.bucket_id=bucket AND (g.permission=permission OR g.permission='read_write'))))));
$$;
CREATE OR REPLACE FUNCTION fence_object_bucket_account_cleanup() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE current_status text;
BEGIN
 SELECT status INTO current_status FROM accounts WHERE id=NEW.account_id FOR UPDATE;
 IF current_status IS DISTINCT FROM 'active' THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_bucket_account_cleanup_fenced',MESSAGE='Inactive account cannot reserve new object buckets';
 END IF;
 RETURN NEW;
END $$;
-- Preserve existing proof, writer and protection fences on durable namespaces.
DO $$ DECLARE signature text; declaration text;
BEGIN
 FOREACH signature IN ARRAY ARRAY['fence_object_lifecycle_deletion()','fence_object_lifecycle_multipart()','fence_object_lifecycle_protection()','fence_object_upload_capture_admission()','fence_object_write_key()','fence_untracked_object_lock_write()','guard_object_multipart_initiation_dispatch()','guard_object_multipart_part_writer()','protect_object_s3_copy_source_epoch()','protect_object_version_protection()','protect_object_write_protection()'] LOOP
  SELECT pg_get_functiondef(to_regprocedure(signature)) INTO declaration;
  EXECUTE replace(declaration,'FROM resource_namespaces ','FROM accounts ');
 END LOOP;
END $$;

DROP TRIGGER object_storage_billing_origin ON orgs;
DROP FUNCTION guard_object_storage_billing_origin();
DROP TRIGGER object_usage_adoption ON orgs;
DROP FUNCTION invalidate_object_usage_on_adoption();
DROP TRIGGER org_object_buckets_guard ON orgs;
DROP FUNCTION guard_org_object_buckets();
DROP TRIGGER object_storage_member_revocation ON org_memberships;
DROP FUNCTION revoke_object_storage_member_keys();
DROP TRIGGER object_storage_issuer ON object_storage_s3_credentials;
DROP FUNCTION stamp_object_storage_issuer();
DROP FUNCTION object_storage_credential_live(uuid);
DROP TRIGGER object_bucket_organization ON object_buckets;
DROP FUNCTION bind_object_bucket_organization();
DROP FUNCTION object_storage_usage_subject(uuid,uuid);
DROP FUNCTION object_storage_usage_subject(uuid,uuid,timestamptz);
DROP FUNCTION object_storage_key_live(uuid,uuid);
ALTER TABLE object_buckets DROP CONSTRAINT object_buckets_account_id_fkey;
ALTER TABLE object_buckets ADD CONSTRAINT object_buckets_account_id_fkey FOREIGN KEY(account_id) REFERENCES accounts(id);
ALTER TABLE object_s3_copy_source_epochs DROP CONSTRAINT object_s3_copy_source_epochs_account_id_fkey;
ALTER TABLE object_s3_copy_source_epochs ADD CONSTRAINT object_s3_copy_source_epochs_account_id_fkey FOREIGN KEY(account_id) REFERENCES accounts(id) ON DELETE CASCADE;
ALTER TABLE object_s3_copy_source_grants DROP CONSTRAINT object_s3_copy_source_grants_account_id_fkey;
ALTER TABLE object_s3_copy_source_grants ADD CONSTRAINT object_s3_copy_source_grants_account_id_fkey FOREIGN KEY(account_id) REFERENCES accounts(id) ON DELETE CASCADE;
ALTER TABLE object_storage_access_grants DROP CONSTRAINT object_storage_access_grants_account_id_fkey;
ALTER TABLE object_storage_access_grants ADD CONSTRAINT object_storage_access_grants_account_id_fkey FOREIGN KEY(account_id) REFERENCES accounts(id) ON DELETE CASCADE;
ALTER TABLE object_storage_authorizations DROP CONSTRAINT object_storage_authorizations_account_id_fkey;
ALTER TABLE object_storage_authorizations ADD CONSTRAINT object_storage_authorizations_account_id_fkey FOREIGN KEY(account_id) REFERENCES accounts(id) ON DELETE CASCADE;
ALTER TABLE object_storage_billing_periods DROP CONSTRAINT object_storage_billing_periods_account_id_fkey;
ALTER TABLE object_storage_billing_periods ADD CONSTRAINT object_storage_billing_periods_account_id_fkey FOREIGN KEY(account_id) REFERENCES accounts(id) ON DELETE CASCADE;
ALTER TABLE object_storage_customer_usage_reports_v2 DROP CONSTRAINT object_storage_customer_usage_reports_v2_account_id_fkey;
ALTER TABLE object_storage_customer_usage_reports_v2 ADD CONSTRAINT object_storage_customer_usage_reports_v2_account_id_fkey FOREIGN KEY(account_id) REFERENCES accounts(id) ON DELETE CASCADE;
ALTER TABLE object_storage_multipart_uploads DROP CONSTRAINT object_storage_multipart_uploads_account_id_fkey;
ALTER TABLE object_storage_multipart_uploads ADD CONSTRAINT object_storage_multipart_uploads_account_id_fkey FOREIGN KEY(account_id) REFERENCES accounts(id) ON DELETE CASCADE;
ALTER TABLE object_storage_s3_credentials DROP CONSTRAINT object_storage_s3_credentials_account_id_fkey;
ALTER TABLE object_storage_s3_credentials ADD CONSTRAINT object_storage_s3_credentials_account_id_fkey FOREIGN KEY(account_id) REFERENCES accounts(id) ON DELETE CASCADE;
ALTER TABLE object_storage_upload_grants DROP CONSTRAINT object_storage_upload_grants_account_id_fkey;
ALTER TABLE object_storage_upload_grants ADD CONSTRAINT object_storage_upload_grants_account_id_fkey FOREIGN KEY(account_id) REFERENCES accounts(id) ON DELETE CASCADE;
ALTER TABLE object_storage_usage_reports DROP CONSTRAINT object_storage_usage_reports_account_id_fkey;
ALTER TABLE object_storage_usage_reports ADD CONSTRAINT object_storage_usage_reports_account_id_fkey FOREIGN KEY(account_id) REFERENCES accounts(id) ON DELETE CASCADE;
ALTER TABLE object_upload_completions DROP CONSTRAINT object_upload_completions_account_id_fkey;
ALTER TABLE object_upload_completions ADD CONSTRAINT object_upload_completions_account_id_fkey FOREIGN KEY(account_id) REFERENCES accounts(id) ON DELETE CASCADE;
ALTER TABLE object_upload_routes DROP CONSTRAINT object_upload_routes_account_id_fkey;
ALTER TABLE object_upload_routes ADD CONSTRAINT object_upload_routes_account_id_fkey FOREIGN KEY(account_id) REFERENCES accounts(id) ON DELETE CASCADE;
ALTER TABLE object_version_protection DROP CONSTRAINT object_version_protection_account_id_fkey;
ALTER TABLE object_version_protection ADD CONSTRAINT object_version_protection_account_id_fkey FOREIGN KEY(account_id) REFERENCES accounts(id) ON DELETE CASCADE;
CREATE OR REPLACE FUNCTION org_app_identity_erasure_blocked(identity_id uuid) RETURNS boolean
    LANGUAGE plpgsql STABLE
    AS $_$
DECLARE resource_table text; found boolean;
BEGIN
    IF EXISTS (SELECT 1 FROM org_memberships m JOIN orgs o ON o.id=m.org_id
               WHERE m.account_id=identity_id AND m.removed_at IS NULL
                 AND m.role='owner' AND NOT o.personal_org AND NOT o.deleted_pending) THEN
        RETURN true;
    END IF;
    IF EXISTS (SELECT 1 FROM managed_postgres_databases d JOIN orgs o ON o.id=d.org_id
               WHERE d.account_id=identity_id AND NOT o.personal_org AND NOT o.resource_ownership_enabled) THEN
        RETURN true;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM managed_postgres_databases d JOIN orgs o ON o.id=d.org_id
                   WHERE d.account_id=identity_id AND NOT o.personal_org)
       AND NOT EXISTS (SELECT 1 FROM apps a JOIN orgs o ON o.id=a.org_id
                   WHERE a.account_id=identity_id AND NOT o.personal_org)
       AND NOT EXISTS (SELECT 1 FROM projects p JOIN orgs o ON o.id=p.org_id
                       WHERE p.account_id=identity_id AND NOT o.personal_org) THEN
        RETURN false;
    END IF;
    IF EXISTS (SELECT 1 FROM apps a JOIN orgs o ON o.id=a.org_id
               WHERE a.account_id=identity_id AND NOT o.personal_org
                 AND (a.github_install_id IS NOT NULL OR NOT o.resource_ownership_enabled)) THEN
        RETURN true;
    END IF;
    IF EXISTS (SELECT 1 FROM projects p JOIN orgs o ON o.id=p.org_id
               WHERE p.account_id=identity_id AND NOT o.personal_org
                 AND (coalesce(p.install_id,0) <> 0 OR NOT o.resource_ownership_enabled))
       OR EXISTS (SELECT 1 FROM environment_git_sources g JOIN projects p ON p.id=g.project_id
                  JOIN orgs o ON o.id=p.org_id WHERE p.account_id=identity_id AND NOT o.personal_org) THEN
        RETURN true;
    END IF;
    FOR resource_table IN SELECT unnest(ARRAY[
        'alert_deliveries',
        'alert_rules',
        'api_consumer_plan_admissions',
        'api_consumer_plan_assignments',
        'api_consumer_plans',
        'api_consumer_rate_cards',
        'api_consumer_route_usage_minutes',
        'api_consumer_usage_alerts',
        'api_consumer_usage_events',
        'api_consumer_usage_minutes',
        'api_consumer_usage_statement_handoffs',
        'api_consumer_usage_statements',
        'api_consumers',
        'app_api_routes',
        'app_issues',
        'app_private_network_attachment_nodes',
        'app_private_network_attachments',
        'app_route_priorities',
        'app_route_removal_policies',
        'app_tcp_listeners',
        'app_udp_listeners',
        'app_webhook_deliveries',
        'app_webhook_event_outbox',
        'app_webhooks',
        'application_standard_snapshot_captures',
        'automatic_route_checks',
        'canary_route_gates',
        'ci_automation_branches',
        'ci_automation_event_retries',
        'ci_automation_events',
        'ci_automation_path_checks',
        'ci_automation_policies',
        'ci_automation_pull_requests',
        'ci_execution_archives',
        'ci_execution_check_retries',
        'ci_execution_checks',
        'ci_execution_profiles',
        'ci_executions',
        'ci_github_bindings',
        'ci_github_checks',
        'ci_github_triggers',
        'ci_job_attempts',
        'ci_managed_objects',
        'ci_pipeline_revisions',
        'ci_run_events',
        'ci_runs',
        'ci_source_check_retries',
        'commit_sources',
        'consumer_keys',
        'cors_presets',
        'customer_operation_definitions',
        'customer_operation_idempotency',
        'customer_operation_stream_leases',
        'customer_operation_webhook_bindings',
        'customer_operation_workflow_states',
        'customer_operations',
        'customer_workflow_playbook_progress',
        'customer_workflow_readiness_subscriptions',
        'data_upstreams',
        'dead_letter_events',
        'dev_bridge_sessions',
        'dev_bridge_webhook_replays',
        'edge_rules',
        'entity_outbox_acceptances',
        'event_recovery_jobs',
        'event_subscription_capacity_commands',
        'event_subscription_circuit_breakers',
        'event_subscription_delivery_controls',
        'event_subscriptions',
        'exclusive_work_operations',
        'exclusive_work_trigger_bindings',
        'inbound_webhook_endpoints',
        'invocation_environment_queue_admissions',
        'issue_ingest_tokens',
        'job_runs',
        'managed_realtime_drain_operations',
        'managed_realtime_endpoints',
        'mcp_catalog_monitors',
        'mcp_readiness_attention_actions',
        'mcp_readiness_incidents',
        'mcp_readiness_maintenance_windows',
        'mcp_readiness_notification_actions',
        'app_slos',
        'notification_channels',
        'synthetic_checks',
        'meterd_tenant_surface_cert_expiry_state',
        'mirror_rules',
        'object_buckets',
        'object_s3_copy_source_grants',
        'object_storage_access_grants',
        'object_storage_multipart_uploads',
        'object_storage_s3_credentials',
        'object_storage_upload_grants',
        'object_upload_completions',
        'object_upload_routes',
        'object_version_protection',
        'outbound_app_bindings',
        'platform_tenant_usage_minutes',
        'prewarm_intents',
        'profile_canary_checks',
        'profile_deployment_checks',
        'profile_deployment_policies',
        'profile_investigations',
        'profile_periodic_monitors',
        'profile_route_alert_state',
        'project_environment_clone_checkpoint_postgres_credentials',
        'project_environment_clone_postgres_archives',
        'project_environment_clone_postgres_contents',
        'project_environment_clone_postgres_copy_readers',
        'project_environment_clone_postgres_copy_targets',
        'project_environment_clone_postgres_database_plans',
        'project_environment_clone_postgres_database_sql_pins',
        'project_environment_clone_postgres_imports',
        'project_environment_clone_postgres_inventories',
        'project_environment_clone_postgres_membership_plans',
        'project_environment_clone_postgres_role_plans',
        'project_environment_clone_postgres_snapshot_restores',
        'project_environment_clone_postgres_target_sql_pins',
        'project_environment_clone_postgres_verification_read_budgets',
        'project_environment_clone_postgres_verifications',
        'project_environment_queue_runtime_sets',
        'queue_bindings',
        'reserved_ip_leases',
        'route_check_history',
        'route_health_gates',
        'route_health_history',
        'route_health_notification_state',
        'route_lifecycle_approvals',
        'route_monitor_incidents',
        'route_monitors',
        'route_policy_receipts',
        'route_removal_approvals',
        'runtime_upgrade_operations',
        'saved_route_requirements',
        'product_control_billing_documents',
        'product_control_customer_changes',
        'product_control_customers',
        'product_control_email_subscriptions',
        'product_control_heads',
        'product_control_help_requests',
        'product_control_invoice_handoffs',
        'product_control_notification_reads',
        'product_control_onboardings',
        'product_control_plan_changes',
        'product_control_receipts',
        'product_control_revisions',
        'managed_postgres_connection_capacity',
        'data_api_runtime_connection_observations',
        'data_repair_rehearsals',
        'scenario_cold_preparations',
        'scenario_managed_work',
        'scenario_object_fixtures',
        'scenario_postgres_faults',
        'scenario_postgres_fixtures',
        'scenario_suspend_resumes',
        'scenario_test_action_policies',
        'scenario_test_chaos_matches',
        'scenario_test_members',
        'scenario_worker_faults',
        'schedule_occurrences',
        'tenant_surfaces',
        'triggers',
        'workflow_automation_publish_receipts',
        'workflow_operation_effects',
        'workflow_run_resumes'
    ]::text[]) LOOP
        EXECUTE format('SELECT EXISTS (SELECT 1 FROM %I WHERE account_id = $1)', resource_table)
            INTO found USING identity_id;
        IF found THEN RETURN true; END IF;
    END LOOP;
    RETURN false;
END $_$;
ALTER TABLE object_storage_s3_credentials DROP CONSTRAINT object_s3_issuer_pair;
ALTER TABLE object_storage_s3_credentials DROP COLUMN issuer_account_id,DROP COLUMN issuer_access_revision;
ALTER TABLE object_buckets DROP COLUMN org_id;
ALTER TABLE orgs DROP COLUMN object_storage_owned_from;
-- +goose StatementEnd
RESET lock_timeout;
