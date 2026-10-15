-- filename: 20261002002219091_application_standard_plan_compatibility_resident_only.sql
-- adr: 429/422. Plan compatibility retains existing guests; new boots stay strict.

-- +goose Up
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
   IF NOT application_standard_runtime_inputs_match(captured_input,current_input) OR
     (captured_input->'account_plan' IS DISTINCT FROM current_input->'account_plan' AND OLD.state IN ('waking','cold_booting')) THEN
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
