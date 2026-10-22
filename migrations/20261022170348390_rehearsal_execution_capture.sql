-- filename: 20261022170348390_rehearsal_execution_capture.sql

-- ADR-20261022170348: a contained rehearsal's standalone Run starts and
-- cancellations join the captured platform calls. They are addressed by the
-- execution: prefix, never performed, and read back only inside their run.
-- migration-compat: expand
--
-- The kind and target CHECKs are replaced online: the wider ones are added NOT
-- VALID under new names and validated, then the old ones are dropped. Each
-- step is its own transaction and re-runnable, and a CHECK always holds.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';

-- +goose StatementBegin
DO $checks$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'scenario_captured_platform_calls'::regclass
      AND conname = 'scenario_captured_platform_calls_kind_check_v2') THEN
    ALTER TABLE scenario_captured_platform_calls ADD CONSTRAINT scenario_captured_platform_calls_kind_check_v2
      CHECK (kind IN ('workflow_run', 'workflow_event', 'realtime_publish', 'realtime_signal', 'execution', 'execution_cancel')) NOT VALID;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'scenario_captured_platform_calls'::regclass
      AND conname = 'scenario_captured_platform_calls_target_check_v2') THEN
    ALTER TABLE scenario_captured_platform_calls ADD CONSTRAINT scenario_captured_platform_calls_target_check_v2
      CHECK (target ~ '^(workload|app|run|endpoint|execution):' AND char_length(target) <= 512) NOT VALID;
  END IF;
END
$checks$;
-- +goose StatementEnd

ALTER TABLE scenario_captured_platform_calls VALIDATE CONSTRAINT scenario_captured_platform_calls_kind_check_v2;

ALTER TABLE scenario_captured_platform_calls VALIDATE CONSTRAINT scenario_captured_platform_calls_target_check_v2;

ALTER TABLE scenario_captured_platform_calls DROP CONSTRAINT IF EXISTS scenario_captured_platform_calls_kind_check;

ALTER TABLE scenario_captured_platform_calls DROP CONSTRAINT IF EXISTS scenario_captured_platform_calls_target_check;

-- +goose Down
SET lock_timeout = '5s';

-- +goose StatementBegin
DO $checks$
BEGIN
  IF EXISTS (SELECT 1 FROM scenario_captured_platform_calls WHERE kind IN ('execution', 'execution_cancel')) THEN
    RAISE EXCEPTION 'captured rehearsal executions are retained run evidence';
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'scenario_captured_platform_calls'::regclass
      AND conname = 'scenario_captured_platform_calls_kind_check') THEN
    ALTER TABLE scenario_captured_platform_calls ADD CONSTRAINT scenario_captured_platform_calls_kind_check
      CHECK (kind IN ('workflow_run', 'workflow_event', 'realtime_publish', 'realtime_signal')) NOT VALID;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'scenario_captured_platform_calls'::regclass
      AND conname = 'scenario_captured_platform_calls_target_check') THEN
    ALTER TABLE scenario_captured_platform_calls ADD CONSTRAINT scenario_captured_platform_calls_target_check
      CHECK (target ~ '^(workload|app|run|endpoint):' AND char_length(target) <= 512) NOT VALID;
  END IF;
END
$checks$;
-- +goose StatementEnd

ALTER TABLE scenario_captured_platform_calls VALIDATE CONSTRAINT scenario_captured_platform_calls_kind_check;

ALTER TABLE scenario_captured_platform_calls VALIDATE CONSTRAINT scenario_captured_platform_calls_target_check;

ALTER TABLE scenario_captured_platform_calls DROP CONSTRAINT IF EXISTS scenario_captured_platform_calls_kind_check_v2;

ALTER TABLE scenario_captured_platform_calls DROP CONSTRAINT IF EXISTS scenario_captured_platform_calls_target_check_v2;
