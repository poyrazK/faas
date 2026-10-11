-- filename: 20261005162807808_object_event_hold_protection.sql
-- +goose Up
-- +goose StatementBegin
ALTER TABLE object_version_protection ADD COLUMN IF NOT EXISTS event_hold_baseline jsonb;

CREATE OR REPLACE FUNCTION object_event_hold_retention_valid(r jsonb, writing boolean)
RETURNS boolean LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE p jsonb; n numeric; d timestamptz;
BEGIN
 IF jsonb_typeof(r) IS DISTINCT FROM 'object' OR octet_length(r::text)>16384
  OR EXISTS(SELECT 1 FROM jsonb_object_keys(r) AS k WHERE k NOT IN ('mode','retain_until_date','event_hold','event_hold_duration')) THEN RETURN false; END IF;
 IF r='{}'::jsonb THEN RETURN true; END IF;
 IF jsonb_typeof(r->'mode') IS DISTINCT FROM 'string' OR r->>'mode' NOT IN ('COMPLIANCE','GOVERNANCE') THEN RETURN false; END IF;
 IF r ? 'retain_until_date' THEN
  IF jsonb_typeof(r->'retain_until_date') IS DISTINCT FROM 'string' OR r->>'retain_until_date' !~ '^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}([.][0-9]+)?(Z|[+-][0-9]{2}:[0-9]{2})$' THEN RETURN false; END IF;
  d:=(r->>'retain_until_date')::timestamptz;
  IF NOT isfinite(d) OR extract(year FROM d) NOT BETWEEN 1 AND 9999 THEN RETURN false; END IF;
 END IF;
 IF r ? 'event_hold' THEN
  IF jsonb_typeof(r->'event_hold') IS DISTINCT FROM 'string' OR r->>'event_hold' NOT IN ('ON','OFF') THEN RETURN false; END IF;
 ELSE
  RETURN r ? 'retain_until_date' AND NOT r ? 'event_hold_duration';
 END IF;
 IF r ? 'event_hold_duration' THEN
  p:=r->'event_hold_duration';
  IF jsonb_typeof(p) IS DISTINCT FROM 'object' OR (SELECT count(*) FROM jsonb_object_keys(p))<>1 OR NOT (p ? 'days' OR p ? 'years') THEN RETURN false; END IF;
  IF jsonb_typeof(coalesce(p->'days',p->'years')) IS DISTINCT FROM 'number' OR coalesce(p->>'days',p->>'years') !~ '^[0-9]+$' THEN RETURN false; END IF;
  n:=coalesce(p->>'days',p->>'years')::numeric;
  IF n<1 OR n>(CASE WHEN p ? 'days' THEN 36500 ELSE 100 END) THEN RETURN false; END IF;
 END IF;
 IF r->>'event_hold'='ON' AND NOT r ? 'event_hold_duration' THEN RETURN false; END IF;
 IF writing THEN RETURN r->>'event_hold'<>'OFF' OR NOT r ? 'event_hold_duration'; END IF;
 RETURN r ? 'retain_until_date';
EXCEPTION WHEN invalid_datetime_format OR datetime_field_overflow OR numeric_value_out_of_range THEN RETURN false;
END $$;

-- Replace only the original intent-policy shape check. Its generated name is
-- deliberately discovered so replay does not depend on constraint numbering.
DO $$ DECLARE c record; BEGIN
 FOR c IN SELECT conname FROM pg_constraint WHERE conrelid='object_version_protection'::regclass AND contype='c'
  AND pg_get_constraintdef(oid) LIKE '%event_hold_duration%' LOOP
  EXECUTE format('ALTER TABLE object_version_protection DROP CONSTRAINT %I',c.conname);
 END LOOP;
END $$;
ALTER TABLE object_version_protection DROP CONSTRAINT IF EXISTS object_version_protection_policy_shape;
ALTER TABLE object_version_protection ADD CONSTRAINT object_version_protection_policy_shape CHECK(coalesce(
 (intent->>'kind'='legal_hold' AND NOT intent ? 'retention' AND jsonb_typeof(intent->'legal_hold')='object' AND intent->'legal_hold'->>'status' IN ('ON','OFF')) OR
 (intent->>'kind'='retention' AND NOT intent ? 'legal_hold' AND object_event_hold_retention_valid(intent->'retention',true)),false));
ALTER TABLE object_version_protection DROP CONSTRAINT IF EXISTS object_version_protection_event_baseline;
ALTER TABLE object_version_protection ADD CONSTRAINT object_version_protection_event_baseline CHECK(
 (event_hold_baseline IS NULL OR coalesce(intent->>'kind'='retention' AND intent->'retention'->>'event_hold' IN ('ON','OFF') AND object_event_hold_retention_valid(event_hold_baseline,false)
  AND (intent->'retention'->>'event_hold'<>'OFF' OR intent->'retention' ? 'retain_until_date' OR event_hold_baseline->>'event_hold'='ON'),false))
 AND (NOT coalesce(intent->'retention'->>'event_hold' IN ('ON','OFF'),false) OR NOT (dispatched OR state='ready') OR event_hold_baseline IS NOT NULL));

-- This independent guard survives replay of the old journal trigger. It also
-- fences older writers that omit or clear the newly required release evidence.
CREATE OR REPLACE FUNCTION protect_object_event_hold_baseline() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='INSERT' THEN
  IF NEW.event_hold_baseline IS NOT NULL THEN RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Event hold baseline requires a live worker lease'; END IF;
 ELSIF NEW.event_hold_baseline IS DISTINCT FROM OLD.event_hold_baseline THEN
  IF OLD.event_hold_baseline IS NOT NULL OR OLD.dispatched OR NEW.dispatched OR OLD.state<>'applying' OR NEW.state<>'applying'
   OR OLD.lease_token='' OR NEW.lease_token<>OLD.lease_token OR OLD.lease_until<=clock_timestamp() THEN
   RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Preserve immutable event hold baseline';
  END IF;
 END IF;
 RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS object_event_hold_baseline_guard ON object_version_protection;
CREATE TRIGGER object_event_hold_baseline_guard BEFORE INSERT OR UPDATE ON object_version_protection FOR EACH ROW EXECUTE FUNCTION protect_object_event_hold_baseline();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM object_version_protection WHERE event_hold_baseline IS NOT NULL OR intent->'retention' ? 'event_hold') THEN
  RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='Preserve event hold receipts before rollback';
 END IF;
END $$;
DROP TRIGGER IF EXISTS object_event_hold_baseline_guard ON object_version_protection;
DROP FUNCTION IF EXISTS protect_object_event_hold_baseline();
ALTER TABLE object_version_protection DROP CONSTRAINT IF EXISTS object_version_protection_event_baseline;
ALTER TABLE object_version_protection DROP CONSTRAINT IF EXISTS object_version_protection_policy_shape;
ALTER TABLE object_version_protection ADD CONSTRAINT object_version_protection_policy_shape CHECK(coalesce(
 (intent->>'kind'='legal_hold' AND NOT intent ? 'retention' AND jsonb_typeof(intent->'legal_hold')='object' AND intent->'legal_hold'->>'status' IN ('ON','OFF')) OR
 (intent->>'kind'='retention' AND NOT intent ? 'legal_hold' AND jsonb_typeof(intent->'retention')='object' AND NOT intent->'retention' ? 'event_hold' AND NOT intent->'retention' ? 'event_hold_duration' AND
 (intent->'retention'='{}'::jsonb OR (intent->'retention'->>'mode' IN ('GOVERNANCE','COMPLIANCE') AND jsonb_typeof(intent->'retention'->'retain_until_date')='string'))),false));
ALTER TABLE object_version_protection DROP COLUMN IF EXISTS event_hold_baseline;
DROP FUNCTION IF EXISTS object_event_hold_retention_valid(jsonb,boolean);
-- +goose StatementEnd
