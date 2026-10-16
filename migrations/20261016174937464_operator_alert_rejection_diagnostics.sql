-- filename: 20261016174937464_operator_alert_rejection_diagnostics.sql

-- +goose Up
-- Existing and unreadable attempts deliberately retain NULL diagnostics.
ALTER TABLE operator_alert_collection_outcomes
    ADD COLUMN IF NOT EXISTS diagnostics jsonb;
-- +goose StatementBegin
DO $$ BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'operator_alert_collection_outcomes'::regclass AND conname = 'operator_alert_collection_diagnostics_check') THEN
        ALTER TABLE operator_alert_collection_outcomes
            ADD CONSTRAINT operator_alert_collection_diagnostics_check CHECK (
                diagnostics IS NULL OR (
                    outcome = 'failure' AND reason = 'snapshot_rejected'
                    AND jsonb_typeof(diagnostics) = 'object'
                    AND octet_length(diagnostics::text) <= 32768
                    AND diagnostics @> '{"scope":"rejected_snapshot"}'::jsonb
                )
            );
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
ALTER TABLE operator_alert_collection_outcomes
    DROP CONSTRAINT IF EXISTS operator_alert_collection_diagnostics_check,
    DROP COLUMN IF EXISTS diagnostics;
