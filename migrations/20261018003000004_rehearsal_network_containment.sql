-- +goose Up
SET LOCAL lock_timeout = '5s';
-- A missing ledger row may leave this migration's complete schema behind.
-- Preserve later fences only when every owned object and a known function
-- definition are present; refuse partial or unrecognized schema.
-- +goose StatementBegin
DO $rehearsal_replay$
BEGIN
  IF EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid=to_regclass('scenario_test_action_policies') AND tgname='scenario_network_policy_immutable' AND NOT tgisinternal)
    AND EXISTS (SELECT 1 FROM pg_attribute WHERE attrelid=to_regclass('scenario_test_action_policies') AND attname='network_mode' AND attnum>0 AND NOT attisdropped)
    AND EXISTS (SELECT 1 FROM pg_proc WHERE oid=to_regprocedure('scenario_network_policy_immutable()') AND md5(prosrc)=ANY(ARRAY['e1b6dca801b081bbb0eed265c96714f1'])) THEN
    RETURN;
  END IF;
  IF EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid=to_regclass('scenario_test_action_policies') AND tgname='scenario_network_policy_immutable' AND NOT tgisinternal)
    OR EXISTS (SELECT 1 FROM pg_attribute WHERE attrelid=to_regclass('scenario_test_action_policies') AND attname='network_mode' AND attnum>0 AND NOT attisdropped)
    OR EXISTS (SELECT 1 FROM pg_proc WHERE oid=to_regprocedure('scenario_network_policy_immutable()') AND md5(prosrc)=ANY(ARRAY['e1b6dca801b081bbb0eed265c96714f1'])) THEN
    RAISE EXCEPTION 'partial or unrecognized schema for rehearsal migration 4; inspect recovery before continuing';
  END IF;

ALTER TABLE scenario_test_action_policies ADD COLUMN network_mode text NOT NULL DEFAULT ''
    CHECK (network_mode IN ('', 'contained'));

-- Retained enrollment can never become an unrestricted runtime after cleanup,
-- policy edits or a rollback. Snapshot sealing may still update its own fields.
CREATE FUNCTION scenario_network_policy_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF (NEW.app_id, NEW.account_id, NEW.run_id, NEW.network_mode)
       IS DISTINCT FROM (OLD.app_id, OLD.account_id, OLD.run_id, OLD.network_mode) THEN
        RAISE EXCEPTION 'rehearsal network policy is immutable' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END $$;
CREATE TRIGGER scenario_network_policy_immutable BEFORE UPDATE ON scenario_test_action_policies
    FOR EACH ROW EXECUTE FUNCTION scenario_network_policy_immutable();
END
$rehearsal_replay$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM scenario_test_action_policies WHERE network_mode = 'contained') THEN
        RAISE EXCEPTION 'cannot remove rehearsal containment while enrolled apps remain';
    END IF;
END $$;
-- +goose StatementEnd
DROP TRIGGER scenario_network_policy_immutable ON scenario_test_action_policies;
DROP FUNCTION scenario_network_policy_immutable();
ALTER TABLE scenario_test_action_policies DROP COLUMN network_mode;
