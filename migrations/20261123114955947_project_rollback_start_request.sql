-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';

ALTER TABLE project_rollback_operations
    ADD COLUMN IF NOT EXISTS start_request_id uuid,
    ADD COLUMN IF NOT EXISTS start_request_options jsonb;

-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'project_rollback_operations'::regclass
          AND conname = 'project_rollback_start_request_pair'
    ) THEN
        ALTER TABLE project_rollback_operations
            ADD CONSTRAINT project_rollback_start_request_pair
            CHECK ((start_request_id IS NULL) = (start_request_options IS NULL));
    END IF;
END $$;
-- +goose StatementEnd

CREATE UNIQUE INDEX IF NOT EXISTS project_rollback_start_request_once
    ON project_rollback_operations (project_id, start_request_id)
    WHERE start_request_id IS NOT NULL;

-- Keep the public request ID on receipts saved by mixed-version workers.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION retain_project_rollback_start_request() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.start_request_id IS NOT NULL THEN
        NEW.receipt := NEW.receipt || jsonb_build_object('request_id', NEW.start_request_id);
    END IF;
    RETURN NEW;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_trigger
        WHERE tgrelid = 'project_rollback_operations'::regclass AND tgname = 'retain_project_rollback_start_request'
    ) THEN
        CREATE TRIGGER retain_project_rollback_start_request
            BEFORE INSERT OR UPDATE OF receipt ON project_rollback_operations
            FOR EACH ROW EXECUTE FUNCTION retain_project_rollback_start_request();
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TRIGGER retain_project_rollback_start_request ON project_rollback_operations;
DROP FUNCTION retain_project_rollback_start_request();
DROP INDEX project_rollback_start_request_once;
ALTER TABLE project_rollback_operations
    DROP CONSTRAINT project_rollback_start_request_pair,
    DROP COLUMN start_request_id,
    DROP COLUMN start_request_options;
