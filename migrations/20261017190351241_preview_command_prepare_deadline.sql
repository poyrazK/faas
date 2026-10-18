-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
ALTER TABLE financial_preview_command_runtimes ADD COLUMN IF NOT EXISTS prepare_until timestamptz;
UPDATE financial_preview_command_runtimes SET prepare_until=recorded_at+interval '2 minutes' WHERE prepare_until IS NULL;
ALTER TABLE financial_preview_command_runtimes ALTER COLUMN prepare_until SET NOT NULL;
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='financial_preview_command_runtimes'::regclass AND conname='financial_preview_command_prepare_deadline_check') THEN
  ALTER TABLE financial_preview_command_runtimes ADD CONSTRAINT financial_preview_command_prepare_deadline_check CHECK (prepare_until>=recorded_at);
 END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
ALTER TABLE financial_preview_command_runtimes DROP COLUMN prepare_until;

-- migration-compat: contract
-- Financial funding rollout coordinates scheduler, native-owner and meter
-- capabilities with the USD gate; this batch requires a full release.
