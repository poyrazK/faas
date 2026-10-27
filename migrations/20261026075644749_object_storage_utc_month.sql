-- migration-compat: expand
-- filename: 20261026075644749_object_storage_utc_month.sql
--
-- Object storage periods are UTC months (state.ObjectStoragePeriod and the
-- 3-argument object_storage_usage_subject). Two functions truncated now() in
-- the session time zone instead, so on a database whose TimeZone is not UTC a
-- company bucket was attributed to its creator around a month boundary and
-- in the month its organization adopted it, and admission could not find the
-- bucket in the owner's usage. Only the truncation changes.

-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
CREATE OR REPLACE FUNCTION object_storage_usage_subject(owner_org uuid, namespace uuid) RETURNS uuid
    LANGUAGE sql STABLE
    AS $$
 SELECT object_storage_usage_subject(owner_org,namespace,date_trunc('month',now() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC')
$$;
CREATE OR REPLACE FUNCTION invalidate_object_usage_on_adoption() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
 IF NEW.resource_ownership_enabled AND (TG_OP='INSERT' OR NOT OLD.resource_ownership_enabled) THEN
  IF NEW.object_storage_owned_from IS NULL THEN
   UPDATE orgs SET object_storage_owned_from=now() WHERE id=NEW.id;
  END IF;
  IF EXISTS(SELECT 1 FROM object_storage_billing_periods p WHERE p.period_start>=date_trunc('month',now() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'
    AND EXISTS(SELECT 1 FROM object_buckets b WHERE b.org_id=NEW.id AND b.account_id=p.account_id)) THEN
   RAISE EXCEPTION 'remediate finalized current-period object billing before adoption' USING ERRCODE='23514';
  END IF;
  DELETE FROM object_storage_usage_heads h WHERE h.period_start>=date_trunc('month',now() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC' AND
   (h.account_id=NEW.id OR EXISTS(SELECT 1 FROM object_buckets b WHERE b.org_id=NEW.id AND b.account_id=h.account_id));
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
CREATE OR REPLACE FUNCTION object_storage_usage_subject(owner_org uuid, namespace uuid) RETURNS uuid
    LANGUAGE sql STABLE
    AS $$
 SELECT object_storage_usage_subject(owner_org,namespace,date_trunc('month',now()))
$$;
CREATE OR REPLACE FUNCTION invalidate_object_usage_on_adoption() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
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
