-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE scenario_test_action_policies
 ADD COLUMN workload_name text NOT NULL DEFAULT '' CHECK (workload_name = '' OR workload_name ~ '^[a-z0-9]([a-z0-9-]{1,38})[a-z0-9]$'),
 ADD COLUMN sealed_at timestamptz,
 ADD COLUMN sealed_snapshot jsonb CHECK (sealed_snapshot IS NULL OR (jsonb_typeof(sealed_snapshot) = 'object' AND octet_length(sealed_snapshot::text) <= 2097152)),
 ADD CONSTRAINT scenario_action_snapshot_sealed CHECK (sealed_snapshot IS NULL OR sealed_at IS NOT NULL);
UPDATE scenario_test_action_policies p SET workload_name = m.workload_name
FROM scenario_test_members m WHERE m.app_id = p.app_id AND m.account_id = p.account_id AND m.run_id = p.run_id;

ALTER TABLE exclusive_work_effects ADD COLUMN receiver_sha256 text NOT NULL DEFAULT ''
 CHECK (receiver_sha256 = '' OR (rehearsal_run_id <> '' AND receiver_sha256 ~ '^[0-9a-f]{64}$'));
ALTER TABLE workflow_operation_effects ADD COLUMN receiver_sha256 text NOT NULL DEFAULT ''
 CHECK (receiver_sha256 = '' OR (rehearsal_run_id <> '' AND receiver_sha256 ~ '^[0-9a-f]{64}$'));

-- A sealed receipt must stay closed even while older completion workers run.
-- The existing app-scope locks serialize completion with sealing. This fence
-- also refuses legacy inserts which omit the rehearsal marker entirely.
-- +goose StatementBegin
CREATE FUNCTION scenario_action_sealed_effect_fence() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE source_app uuid;
BEGIN
 IF TG_TABLE_NAME = 'exclusive_work_effects' THEN
  SELECT app_id INTO source_app FROM exclusive_work_operations WHERE id = NEW.operation_id;
 ELSE
  source_app := NEW.app_id;
 END IF;
 PERFORM id FROM apps WHERE id = source_app FOR KEY SHARE;
 IF EXISTS (SELECT 1 FROM scenario_test_action_policies WHERE app_id = source_app AND sealed_at IS NOT NULL) THEN
  RAISE EXCEPTION 'scenario action window is sealed' USING ERRCODE = '23514';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER scenario_action_sealed_effect_fence BEFORE INSERT OR UPDATE ON exclusive_work_effects
 FOR EACH ROW EXECUTE FUNCTION scenario_action_sealed_effect_fence();
CREATE TRIGGER scenario_action_sealed_effect_fence BEFORE INSERT OR UPDATE ON workflow_operation_effects
 FOR EACH ROW EXECUTE FUNCTION scenario_action_sealed_effect_fence();

-- +goose Down
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM scenario_test_action_policies WHERE sealed_at IS NOT NULL) THEN
  RAISE EXCEPTION 'cannot remove sealed action windows while enrolled apps remain';
 END IF;
END $$;
-- +goose StatementEnd
DROP TRIGGER scenario_action_sealed_effect_fence ON workflow_operation_effects;
DROP TRIGGER scenario_action_sealed_effect_fence ON exclusive_work_effects;
DROP FUNCTION scenario_action_sealed_effect_fence();
ALTER TABLE workflow_operation_effects DROP COLUMN receiver_sha256;
ALTER TABLE exclusive_work_effects DROP COLUMN receiver_sha256;
ALTER TABLE scenario_test_action_policies DROP CONSTRAINT scenario_action_snapshot_sealed,
 DROP COLUMN sealed_snapshot, DROP COLUMN sealed_at, DROP COLUMN workload_name;
