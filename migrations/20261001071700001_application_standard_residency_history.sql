-- +goose Up
-- Cleanup must not erase a saved authority pointer or let a historical paused
-- receipt recreate residency after its owner has left the serving states.
-- +goose StatementBegin
CREATE FUNCTION application_standard_native_residency_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE managed boolean;
BEGIN
 IF OLD.app_id IS NULL OR OLD.kind<>'wake' THEN RETURN NEW; END IF;
 SELECT coalesce(input_snapshot->'adoptions'<>'[]'::jsonb OR input_snapshot->'materialized_fields'<>'[]'::jsonb,false)
  INTO managed FROM instance_application_standard_admissions WHERE instance_id=OLD.id;
 IF NOT coalesce(managed,false) THEN RETURN NEW; END IF;
 IF (OLD.application_standard_boot_token IS NOT NULL AND NEW.application_standard_boot_token IS DISTINCT FROM OLD.application_standard_boot_token)
  OR (OLD.application_standard_promotion_token IS NOT NULL AND NEW.application_standard_promotion_token IS DISTINCT FROM OLD.application_standard_promotion_token) THEN
  RAISE EXCEPTION 'native authority history is immutable' USING ERRCODE='23514',CONSTRAINT='application_standard_boot_immutable';
 END IF;
 IF NEW.state IN ('running','warm','migrating') AND OLD.state NOT IN ('waking','cold_booting','running','warm','migrating') THEN
  RAISE EXCEPTION 'historical native receipt cannot recreate residency' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER application_standard_c_native_residency BEFORE UPDATE OF state,application_standard_boot_token,application_standard_promotion_token ON instances
 FOR EACH ROW EXECUTE FUNCTION application_standard_native_residency_guard();

-- +goose Down
-- Retain fail-closed history guards.
