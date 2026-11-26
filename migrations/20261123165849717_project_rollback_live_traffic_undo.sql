-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';

-- An older worker cannot discard the option or link an undo while treating
-- a live-traffic trigger as a probe trigger.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION preserve_project_rollback_live_traffic_undo() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.receipt->>'undo_on_live_traffic_failure' = 'true' AND (
        NEW.receipt->>'undo_on_live_traffic_failure' IS DISTINCT FROM 'true'
        OR (
            OLD.receipt #>> '{auto_undo,trigger}' = 'live_traffic'
            AND NEW.receipt ? 'auto_undo'
            AND ((NEW.receipt->'auto_undo') - 'status' - 'problem')
                IS DISTINCT FROM ((OLD.receipt->'auto_undo') - 'status' - 'problem')
        )
    ) THEN
        RETURN NULL;
    END IF;
    RETURN NEW;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_trigger
        WHERE tgrelid = 'project_rollback_operations'::regclass AND tgname = 'preserve_project_rollback_live_traffic_undo'
    ) THEN
        CREATE TRIGGER preserve_project_rollback_live_traffic_undo
            BEFORE UPDATE OF receipt ON project_rollback_operations
            FOR EACH ROW EXECUTE FUNCTION preserve_project_rollback_live_traffic_undo();
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TRIGGER preserve_project_rollback_live_traffic_undo ON project_rollback_operations;
DROP FUNCTION preserve_project_rollback_live_traffic_undo();
