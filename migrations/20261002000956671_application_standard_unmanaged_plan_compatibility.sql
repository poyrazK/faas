-- filename: 20261002000956671_application_standard_unmanaged_plan_compatibility.sql
-- adr: 429/422. Preserve unmanaged guest history across an account plan change.

-- +goose Up
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION application_standard_runtime_inputs_match(captured jsonb, current_input jsonb) RETURNS boolean
 LANGUAGE sql IMMUTABLE AS $$
 SELECT coalesce(jsonb_typeof(captured)='object' AND jsonb_typeof(current_input)='object' AND
   CASE WHEN captured->'adoptions'='[]'::jsonb AND captured->'materialized_fields'='[]'::jsonb
     AND current_input->'adoptions'='[]'::jsonb AND current_input->'materialized_fields'='[]'::jsonb
     AND captured ? 'account_plan' AND current_input ? 'account_plan'
   THEN captured-'account_plan'=current_input-'account_plan'
   ELSE captured=current_input END,false);
$$;

CREATE OR REPLACE FUNCTION application_standard_instance_runtime_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE current_input jsonb; captured_input jsonb;
BEGIN
 IF TG_OP='UPDATE' AND OLD.kind='wake' AND OLD.app_id IS NOT NULL AND
   (NEW.app_id IS DISTINCT FROM OLD.app_id OR NEW.deployment_id IS DISTINCT FROM OLD.deployment_id OR NEW.kind IS DISTINCT FROM OLD.kind) THEN
  RAISE EXCEPTION 'runtime application and artifact identity are immutable'
   USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_identity';
 END IF;
 IF NEW.app_id IS NULL OR NEW.kind <> 'wake' THEN RETURN NEW; END IF;
 -- Cleanup, bookkeeping and nonresident fixtures remain possible while the
 -- standard is pending. Entry into boot, serving, warm and migration is gated.
 IF NEW.state NOT IN ('waking','cold_booting','running','warm','migrating') THEN RETURN NEW; END IF;
 IF application_standard_runtime_is_unowned(NEW.app_id) THEN RETURN NEW; END IF;
 current_input := application_standard_native_runtime_snapshot(NEW.app_id,NEW.deployment_id) ||
   jsonb_build_object('instance_ram_mb',NEW.ram_mb,'instance_mode',NEW.mode);
 IF TG_OP='INSERT' THEN
  IF NEW.state IN ('running','warm','migrating') AND
    (current_input->'adoptions' <> '[]'::jsonb OR current_input->'materialized_fields' <> '[]'::jsonb) THEN
   RAISE EXCEPTION 'managed runtime requires a captured boot attempt'
    USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
  END IF;
 ELSE
  SELECT input_snapshot INTO captured_input FROM instance_application_standard_admissions WHERE instance_id=NEW.id;
  IF captured_input IS NULL THEN
   -- A legacy instance can continue only while it is still unmanaged. A new
   -- standard requires a fresh instance, never a fabricated historical capture.
   IF current_input->'adoptions' <> '[]'::jsonb OR current_input->'materialized_fields' <> '[]'::jsonb THEN
    RAISE EXCEPTION 'managed runtime has no admission capture'
     USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
   END IF;
  ELSE
   -- Legacy unmanaged captures have no native revision; they gain no native
   -- grant authority from this compatibility comparison.
   IF NOT (captured_input ? 'egress_revision') AND current_input->'adoptions'='[]'::jsonb AND current_input->'materialized_fields'='[]'::jsonb THEN
    current_input:=current_input-'egress_revision';
   END IF;
   IF NOT application_standard_runtime_inputs_match(captured_input,current_input) THEN
   RAISE EXCEPTION 'runtime inputs changed after admission'
    USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
   END IF;
  END IF;
 END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION application_standard_instance_runtime_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE current_input jsonb; captured_input jsonb;
BEGIN
 IF TG_OP='UPDATE' AND OLD.kind='wake' AND OLD.app_id IS NOT NULL AND
   (NEW.app_id IS DISTINCT FROM OLD.app_id OR NEW.deployment_id IS DISTINCT FROM OLD.deployment_id OR NEW.kind IS DISTINCT FROM OLD.kind) THEN
  RAISE EXCEPTION 'runtime application and artifact identity are immutable'
   USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_identity';
 END IF;
 IF NEW.app_id IS NULL OR NEW.kind <> 'wake' THEN RETURN NEW; END IF;
 -- Cleanup, bookkeeping and nonresident fixtures remain possible while the
 -- standard is pending. Entry into boot, serving, warm and migration is gated.
 IF NEW.state NOT IN ('waking','cold_booting','running','warm','migrating') THEN RETURN NEW; END IF;
 IF application_standard_runtime_is_unowned(NEW.app_id) THEN RETURN NEW; END IF;
 current_input := application_standard_native_runtime_snapshot(NEW.app_id,NEW.deployment_id) ||
   jsonb_build_object('instance_ram_mb',NEW.ram_mb,'instance_mode',NEW.mode);
 IF TG_OP='INSERT' THEN
  IF NEW.state IN ('running','warm','migrating') AND
    (current_input->'adoptions' <> '[]'::jsonb OR current_input->'materialized_fields' <> '[]'::jsonb) THEN
   RAISE EXCEPTION 'managed runtime requires a captured boot attempt'
    USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
  END IF;
 ELSE
  SELECT input_snapshot INTO captured_input FROM instance_application_standard_admissions WHERE instance_id=NEW.id;
  IF captured_input IS NULL THEN
   -- A legacy instance can continue only while it is still unmanaged. A new
   -- standard requires a fresh instance, never a fabricated historical capture.
   IF current_input->'adoptions' <> '[]'::jsonb OR current_input->'materialized_fields' <> '[]'::jsonb THEN
    RAISE EXCEPTION 'managed runtime has no admission capture'
     USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
   END IF;
  ELSE
   -- Legacy unmanaged captures have no native revision; they gain no native
   -- grant authority from this compatibility comparison.
   IF NOT (captured_input ? 'egress_revision') AND current_input->'adoptions'='[]'::jsonb AND current_input->'materialized_fields'='[]'::jsonb THEN
    current_input:=current_input-'egress_revision';
   END IF;
   IF captured_input IS DISTINCT FROM current_input THEN
   RAISE EXCEPTION 'runtime inputs changed after admission'
    USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
   END IF;
  END IF;
 END IF;
 RETURN NEW;
END;
$$;
DROP FUNCTION IF EXISTS application_standard_runtime_inputs_match(jsonb,jsonb);
-- +goose StatementEnd
