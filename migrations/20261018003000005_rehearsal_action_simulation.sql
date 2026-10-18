-- +goose Up
SET LOCAL lock_timeout = '5s';
-- A missing ledger row may leave this migration's complete schema behind.
-- Preserve later fences only when every owned object and a known function
-- definition are present; refuse partial or unrecognized schema.
-- +goose StatementBegin
DO $rehearsal_replay$
BEGIN
  IF EXISTS (SELECT 1 FROM pg_class WHERE oid=to_regclass('scenario_simulation_attempts') AND relkind IN ('r','p'))
    AND EXISTS (SELECT 1 FROM pg_class WHERE oid=to_regclass('scenario_simulation_attempts_app') AND relkind IN ('i','I'))
    AND EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid=to_regclass('scenario_test_action_policies') AND tgname='scenario_simulation_policy_immutable' AND NOT tgisinternal)
    AND EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid=to_regclass('scenario_simulation_attempts') AND tgname='scenario_simulation_sealed_fence' AND NOT tgisinternal)
    AND EXISTS (SELECT 1 FROM pg_attribute WHERE attrelid=to_regclass('scenario_test_action_policies') AND attname='simulation_plan' AND attnum>0 AND NOT attisdropped)
    AND EXISTS (SELECT 1 FROM pg_attribute WHERE attrelid=to_regclass('scenario_test_action_policies') AND attname='simulation_failed' AND attnum>0 AND NOT attisdropped)
    AND EXISTS (SELECT 1 FROM pg_proc WHERE oid=to_regprocedure('scenario_simulation_policy_immutable()') AND md5(prosrc)=ANY(ARRAY['c10a74e5cf7f94a480b5a3e5b70f4dc7']))
    AND EXISTS (SELECT 1 FROM pg_proc WHERE oid=to_regprocedure('scenario_simulation_sealed_fence()') AND md5(prosrc)=ANY(ARRAY['1d578c2dfbdcca616108a4620aa6a17e','de16aee16810ee904caa48a18d1b9d57'])) THEN
    RETURN;
  END IF;
  IF EXISTS (SELECT 1 FROM pg_class WHERE oid=to_regclass('scenario_simulation_attempts') AND relkind IN ('r','p'))
    OR EXISTS (SELECT 1 FROM pg_class WHERE oid=to_regclass('scenario_simulation_attempts_app') AND relkind IN ('i','I'))
    OR EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid=to_regclass('scenario_test_action_policies') AND tgname='scenario_simulation_policy_immutable' AND NOT tgisinternal)
    OR EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid=to_regclass('scenario_simulation_attempts') AND tgname='scenario_simulation_sealed_fence' AND NOT tgisinternal)
    OR EXISTS (SELECT 1 FROM pg_attribute WHERE attrelid=to_regclass('scenario_test_action_policies') AND attname='simulation_plan' AND attnum>0 AND NOT attisdropped)
    OR EXISTS (SELECT 1 FROM pg_attribute WHERE attrelid=to_regclass('scenario_test_action_policies') AND attname='simulation_failed' AND attnum>0 AND NOT attisdropped)
    OR EXISTS (SELECT 1 FROM pg_proc WHERE oid=to_regprocedure('scenario_simulation_policy_immutable()') AND md5(prosrc)=ANY(ARRAY['c10a74e5cf7f94a480b5a3e5b70f4dc7']))
    OR EXISTS (SELECT 1 FROM pg_proc WHERE oid=to_regprocedure('scenario_simulation_sealed_fence()') AND md5(prosrc)=ANY(ARRAY['1d578c2dfbdcca616108a4620aa6a17e','de16aee16810ee904caa48a18d1b9d57'])) THEN
    RAISE EXCEPTION 'partial or unrecognized schema for rehearsal migration 5; inspect recovery before continuing';
  END IF;

ALTER TABLE scenario_test_action_policies
 ADD COLUMN simulation_plan jsonb CHECK (simulation_plan IS NULL OR (network_mode='contained' AND jsonb_typeof(simulation_plan)='object' AND octet_length(simulation_plan::text)<=131072)),
 ADD COLUMN simulation_failed boolean NOT NULL DEFAULT false CHECK (NOT simulation_failed OR simulation_plan IS NOT NULL);

CREATE TABLE scenario_simulation_attempts (
 id uuid PRIMARY KEY CHECK (id <> '00000000-0000-0000-0000-000000000000'),
 app_id uuid NOT NULL REFERENCES scenario_test_action_policies(app_id) ON DELETE CASCADE CHECK (app_id <> '00000000-0000-0000-0000-000000000000'),
 evidence jsonb NOT NULL CHECK (jsonb_typeof(evidence)='object' AND octet_length(evidence::text)<=131072),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK (isfinite(created_at))
);
CREATE INDEX scenario_simulation_attempts_app ON scenario_simulation_attempts(app_id);

-- Retained fixture identity cannot be widened after admission. A failure marker
-- can only be raised, including by older/incomplete clients.
CREATE FUNCTION scenario_simulation_policy_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.simulation_plan IS DISTINCT FROM OLD.simulation_plan OR
    (OLD.simulation_failed AND NOT NEW.simulation_failed) THEN
  RAISE EXCEPTION 'rehearsal simulation policy is immutable' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER scenario_simulation_policy_immutable BEFORE UPDATE ON scenario_test_action_policies
 FOR EACH ROW EXECUTE FUNCTION scenario_simulation_policy_immutable();

-- Sealing takes the same app lock before inspecting active requests. This fence
-- prevents late/legacy writers from changing a sealed attempt inventory.
CREATE FUNCTION scenario_simulation_sealed_fence() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 PERFORM id FROM apps WHERE id=NEW.app_id FOR KEY SHARE;
 IF NOT EXISTS(SELECT 1 FROM scenario_test_action_policies WHERE app_id=NEW.app_id AND simulation_plan IS NOT NULL AND sealed_at IS NULL) THEN
  RAISE EXCEPTION 'scenario simulation window is unavailable or sealed' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER scenario_simulation_sealed_fence BEFORE INSERT OR UPDATE ON scenario_simulation_attempts
 FOR EACH ROW EXECUTE FUNCTION scenario_simulation_sealed_fence();
END
$rehearsal_replay$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM scenario_test_action_policies WHERE simulation_plan IS NOT NULL) THEN
  RAISE EXCEPTION 'cannot remove action simulation while enrolled apps remain';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE scenario_simulation_attempts;
DROP FUNCTION scenario_simulation_sealed_fence();
DROP TRIGGER scenario_simulation_policy_immutable ON scenario_test_action_policies;
DROP FUNCTION scenario_simulation_policy_immutable();
ALTER TABLE scenario_test_action_policies DROP COLUMN simulation_plan, DROP COLUMN simulation_failed;
