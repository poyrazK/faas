-- filename: 20261022082721358_rehearsal_captured_invocation_reads.sql

-- ADR-20261022082721: asynchronous invokes join queue sends and delayed tasks
-- as captured invocation work, and a captured delayed task keeps the instant it
-- reported so a read of the capture returns the same schedule. The instant is
-- never compared.
-- migration-compat: expand
--
-- The kind CHECK is replaced online: the wider list is added NOT VALID under a
-- new name and validated, then the old one is dropped. Each step is its own
-- transaction and re-runnable, and a CHECK always holds.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';

ALTER TABLE scenario_captured_queue_messages ADD COLUMN IF NOT EXISTS scheduled_at timestamptz;

-- +goose StatementBegin
DO $kinds$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'scenario_captured_queue_messages'::regclass
      AND conname = 'scenario_captured_queue_messages_kind_check_v2') THEN
    ALTER TABLE scenario_captured_queue_messages ADD CONSTRAINT scenario_captured_queue_messages_kind_check_v2
      CHECK (kind IN ('queue', 'delayed_task', 'async_invoke')) NOT VALID;
  END IF;
END
$kinds$;
-- +goose StatementEnd

ALTER TABLE scenario_captured_queue_messages VALIDATE CONSTRAINT scenario_captured_queue_messages_kind_check_v2;

ALTER TABLE scenario_captured_queue_messages DROP CONSTRAINT IF EXISTS scenario_captured_queue_messages_kind_check;

-- +goose Down
SET lock_timeout = '5s';

-- +goose StatementBegin
DO $kinds$
BEGIN
  IF EXISTS (SELECT 1 FROM scenario_captured_queue_messages WHERE kind = 'async_invoke') THEN
    RAISE EXCEPTION 'captured rehearsal async invokes are retained run evidence';
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'scenario_captured_queue_messages'::regclass
      AND conname = 'scenario_captured_queue_messages_kind_check') THEN
    ALTER TABLE scenario_captured_queue_messages ADD CONSTRAINT scenario_captured_queue_messages_kind_check
      CHECK (kind IN ('queue', 'delayed_task')) NOT VALID;
  END IF;
END
$kinds$;
-- +goose StatementEnd

ALTER TABLE scenario_captured_queue_messages VALIDATE CONSTRAINT scenario_captured_queue_messages_kind_check;

ALTER TABLE scenario_captured_queue_messages DROP CONSTRAINT IF EXISTS scenario_captured_queue_messages_kind_check_v2;

ALTER TABLE scenario_captured_queue_messages DROP COLUMN IF EXISTS scheduled_at;
