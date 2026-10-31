-- migration-compat: expand
-- filename: 20261026163443517_dead_letter_event_route_source.sql
--
-- ADR-20261026163443: a routing failure enters the unified dead-letter ledger
-- as source 'event_route' when its receipt is pruned. This release only lets
-- the ledger hold such rows; nothing writes one until the next release adds
-- the capture, so the previous release never reads one. The new check
-- replaces the old one in three transactions.

-- +goose NO TRANSACTION

-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
DO $$ BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'dead_letter_events'::regclass AND conname = 'dead_letter_events_source_kind_check') THEN
        ALTER TABLE dead_letter_events ADD CONSTRAINT dead_letter_events_source_kind_check
            CHECK (source IN ('invocation', 'trigger_record', 'webhook_delivery', 'job_run', 'workflow_run', 'event_route')) NOT VALID;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
ALTER TABLE dead_letter_events VALIDATE CONSTRAINT dead_letter_events_source_kind_check;
-- +goose StatementEnd

-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
ALTER TABLE dead_letter_events DROP CONSTRAINT IF EXISTS dead_letter_events_source_check;
-- +goose StatementEnd

-- +goose Down
-- The restored check is NOT VALID, so rows written meanwhile can stay.
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
DO $$ BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'dead_letter_events'::regclass AND conname = 'dead_letter_events_source_check') THEN
        ALTER TABLE dead_letter_events ADD CONSTRAINT dead_letter_events_source_check
            CHECK (source IN ('invocation', 'trigger_record', 'webhook_delivery', 'job_run', 'workflow_run')) NOT VALID;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
ALTER TABLE dead_letter_events DROP CONSTRAINT IF EXISTS dead_letter_events_source_kind_check;
-- +goose StatementEnd
