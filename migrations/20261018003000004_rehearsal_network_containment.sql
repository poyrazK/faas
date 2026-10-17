-- +goose Up
ALTER TABLE scenario_test_action_policies ADD COLUMN network_mode text NOT NULL DEFAULT ''
    CHECK (network_mode IN ('', 'contained'));

-- Retained enrollment can never become an unrestricted runtime after cleanup,
-- policy edits or a rollback. Snapshot sealing may still update its own fields.
-- +goose StatementBegin
CREATE FUNCTION scenario_network_policy_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF (NEW.app_id, NEW.account_id, NEW.run_id, NEW.network_mode)
       IS DISTINCT FROM (OLD.app_id, OLD.account_id, OLD.run_id, OLD.network_mode) THEN
        RAISE EXCEPTION 'rehearsal network policy is immutable' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER scenario_network_policy_immutable BEFORE UPDATE ON scenario_test_action_policies
    FOR EACH ROW EXECUTE FUNCTION scenario_network_policy_immutable();

-- +goose Down
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
