-- +goose Up
-- +goose StatementBegin
ALTER TABLE financial_preview_command_runtimes ADD COLUMN prepare_until timestamptz;
UPDATE financial_preview_command_runtimes SET prepare_until=recorded_at+interval '2 minutes';
ALTER TABLE financial_preview_command_runtimes ALTER COLUMN prepare_until SET NOT NULL;
ALTER TABLE financial_preview_command_runtimes ADD CONSTRAINT financial_preview_command_prepare_deadline_check CHECK (prepare_until>=recorded_at);
-- +goose StatementEnd

-- +goose Down
ALTER TABLE financial_preview_command_runtimes DROP COLUMN prepare_until;
