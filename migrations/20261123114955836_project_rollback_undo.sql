-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';

-- A mixed-version controller cannot erase an accepted undo or resume its source.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION preserve_project_rollback_undo_link() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF (OLD.receipt ? 'undo_operation_id' AND (
            NEW.receipt->>'undo_operation_id' IS DISTINCT FROM OLD.receipt->>'undo_operation_id'
            OR NEW.status <> OLD.status
        )) OR (OLD.receipt ? 'undo_of' AND NEW.receipt->>'undo_of' IS DISTINCT FROM OLD.receipt->>'undo_of')
    THEN
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
        WHERE tgrelid = 'project_rollback_operations'::regclass AND tgname = 'preserve_project_rollback_undo_link'
    ) THEN
        CREATE TRIGGER preserve_project_rollback_undo_link
            BEFORE UPDATE OF status, receipt ON project_rollback_operations
            FOR EACH ROW EXECUTE FUNCTION preserve_project_rollback_undo_link();
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TRIGGER preserve_project_rollback_undo_link ON project_rollback_operations;
DROP FUNCTION preserve_project_rollback_undo_link();
