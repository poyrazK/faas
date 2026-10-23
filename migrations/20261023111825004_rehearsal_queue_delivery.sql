-- filename: 20261023111825004_rehearsal_queue_delivery.sql

-- ADR-20261023111825: a contained run may opt in to delivering captured queue
-- sends addressed to a member of the same run as real invocations of that
-- member. The policy keeps the mode; each capture keeps its delivery result.
-- migration-compat: expand
--
-- Both columns are added with a constant default. Their CHECKs are added NOT
-- VALID under their own names and validated, each step its own transaction
-- and re-runnable.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';

ALTER TABLE scenario_test_action_policies ADD COLUMN IF NOT EXISTS queue_delivery text NOT NULL DEFAULT '';

ALTER TABLE scenario_captured_queue_messages ADD COLUMN IF NOT EXISTS delivery text NOT NULL DEFAULT '';

-- +goose StatementBegin
DO $checks$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'scenario_test_action_policies'::regclass
      AND conname = 'scenario_test_action_policies_queue_delivery_check') THEN
    ALTER TABLE scenario_test_action_policies ADD CONSTRAINT scenario_test_action_policies_queue_delivery_check
      CHECK (queue_delivery = '' OR queue_delivery = 'same_run' AND network_mode = 'contained'
        AND managed_work_plan IS NULL AND suspend_resume_spec IS NULL) NOT VALID;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'scenario_captured_queue_messages'::regclass
      AND conname = 'scenario_captured_queue_messages_delivery_check') THEN
    ALTER TABLE scenario_captured_queue_messages ADD CONSTRAINT scenario_captured_queue_messages_delivery_check
      CHECK (delivery IN ('', 'delivered', 'unsupported', 'overflow')) NOT VALID;
  END IF;
END
$checks$;
-- +goose StatementEnd

ALTER TABLE scenario_test_action_policies VALIDATE CONSTRAINT scenario_test_action_policies_queue_delivery_check;

ALTER TABLE scenario_captured_queue_messages VALIDATE CONSTRAINT scenario_captured_queue_messages_delivery_check;

-- +goose Down
SET lock_timeout = '5s';

-- +goose StatementBegin
DO $checks$
BEGIN
  IF EXISTS (SELECT 1 FROM scenario_captured_queue_messages WHERE delivery <> '') THEN
    RAISE EXCEPTION 'delivered rehearsal queue sends are retained run evidence';
  END IF;
END
$checks$;
-- +goose StatementEnd

ALTER TABLE scenario_captured_queue_messages DROP CONSTRAINT IF EXISTS scenario_captured_queue_messages_delivery_check;

ALTER TABLE scenario_test_action_policies DROP CONSTRAINT IF EXISTS scenario_test_action_policies_queue_delivery_check;

ALTER TABLE scenario_captured_queue_messages DROP COLUMN IF EXISTS delivery;

ALTER TABLE scenario_test_action_policies DROP COLUMN IF EXISTS queue_delivery;
