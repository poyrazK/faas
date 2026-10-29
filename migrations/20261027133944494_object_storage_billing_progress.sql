-- filename: 20261027133944494_object_storage_billing_progress.sql
-- migration-compat: expand

-- +goose Up
SET LOCAL lock_timeout = '5s';
-- Object storage is billed in daily steps inside its UTC month, and month
-- close finishes the month (ADR-20261027093220). One row per provider,
-- account, month and mode holds the cumulative charge delivered and at most
-- one pending step; apid writes it with a compare-and-set on version, and
-- meterd reserves live delivered and pending millicents under the overage cap.
-- credited_millicents is what members were already charged for the month
-- when an organization adopted their buckets; only adoption writes it.
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS object_storage_billing_progress (
    provider             text NOT NULL CHECK (provider <> ''),
    account_id           uuid NOT NULL,
    period_start         timestamptz NOT NULL CHECK (
        period_start = date_trunc('month', period_start AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'
    ),
    mode                 text NOT NULL CHECK (mode IN ('shadow', 'live')),
    delivered_millicents bigint NOT NULL CHECK (delivered_millicents BETWEEN 0 AND 1152921504606846976),
    pending_millicents   bigint NOT NULL CHECK (pending_millicents BETWEEN 0 AND 1152921504606846976),
    pending_fixed        boolean NOT NULL,
    credited_millicents  bigint NOT NULL DEFAULT 0 CHECK (credited_millicents BETWEEN 0 AND 1152921504606846976),
    version              bigint NOT NULL CHECK (version > 0),
    updated_at           timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (provider, account_id, period_start, mode),
    CHECK (NOT pending_fixed OR pending_millicents > 0),
    CHECK (delivered_millicents + pending_millicents <= 1152921504606846976)
);
-- +goose StatementEnd
CREATE INDEX IF NOT EXISTS object_storage_billing_progress_account_period_idx
    ON object_storage_billing_progress (account_id, period_start);

COMMENT ON TABLE object_storage_billing_progress IS
    'Object-storage charge delivered so far per provider, account, UTC month and mode. Written by apid in daily steps and at month close; meterd reserves live rows under the overage cap (ADR-20261027093220).';

-- Adoption moves the month's storage to the organization. What its members
-- were already charged for the month becomes the organization's credit, so it
-- owes only the rest. A member's charge can include buckets that stay its
-- own, so the month can be under-billed but never billed twice.
-- +goose StatementBegin
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
  INSERT INTO object_storage_billing_progress AS o
         (provider,account_id,period_start,mode,delivered_millicents,pending_millicents,pending_fixed,credited_millicents,version)
  SELECT p.provider,NEW.id,p.period_start,p.mode,0,0,false,LEAST(SUM(p.delivered_millicents+p.pending_millicents),1152921504606846976),1
    FROM object_storage_billing_progress p
   WHERE p.period_start=date_trunc('month',now() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC' AND p.account_id<>NEW.id
     AND EXISTS(SELECT 1 FROM object_buckets b WHERE b.org_id=NEW.id AND b.account_id=p.account_id)
   GROUP BY p.provider,p.period_start,p.mode
  ON CONFLICT (provider,account_id,period_start,mode) DO UPDATE
     SET credited_millicents=LEAST(o.credited_millicents+EXCLUDED.credited_millicents,1152921504606846976),
         version=o.version+1,updated_at=now();
  DELETE FROM object_storage_usage_heads h WHERE h.period_start>=date_trunc('month',now() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC' AND
   (h.account_id=NEW.id OR EXISTS(SELECT 1 FROM object_buckets b WHERE b.org_id=NEW.id AND b.account_id=h.account_id));
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
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
DROP TABLE IF EXISTS object_storage_billing_progress;
