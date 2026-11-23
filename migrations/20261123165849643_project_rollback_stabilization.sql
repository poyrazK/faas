-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';

-- Cutover already requires saved health evidence for earlier services.
-- An older coordinator must be unable to save a single probe as passed
-- by dropping the stabilization policy from an opted-in receipt.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION preserve_project_rollback_stabilization() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.receipt ? 'stabilize_for_seconds'
       AND NEW.receipt->'stabilize_for_seconds' IS DISTINCT FROM OLD.receipt->'stabilize_for_seconds' THEN
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
        WHERE tgrelid = 'project_rollback_operations'::regclass AND tgname = 'preserve_project_rollback_stabilization'
    ) THEN
        CREATE TRIGGER preserve_project_rollback_stabilization
            BEFORE UPDATE OF receipt ON project_rollback_operations
            FOR EACH ROW EXECUTE FUNCTION preserve_project_rollback_stabilization();
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TRIGGER preserve_project_rollback_stabilization ON project_rollback_operations;
DROP FUNCTION preserve_project_rollback_stabilization();
