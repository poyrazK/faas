-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';

ALTER TABLE project_rollback_operations
    ADD COLUMN start_request_id uuid,
    ADD COLUMN start_request_options jsonb,
    ADD CONSTRAINT project_rollback_start_request_pair
        CHECK ((start_request_id IS NULL) = (start_request_options IS NULL));

CREATE UNIQUE INDEX project_rollback_start_request_once
    ON project_rollback_operations (project_id, start_request_id)
    WHERE start_request_id IS NOT NULL;

-- Keep the public request ID on receipts saved by mixed-version workers.
-- +goose StatementBegin
CREATE FUNCTION retain_project_rollback_start_request() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.start_request_id IS NOT NULL THEN
        NEW.receipt := NEW.receipt || jsonb_build_object('request_id', NEW.start_request_id);
    END IF;
    RETURN NEW;
END $$;
-- +goose StatementEnd

CREATE TRIGGER retain_project_rollback_start_request
    BEFORE INSERT OR UPDATE OF receipt ON project_rollback_operations
    FOR EACH ROW EXECUTE FUNCTION retain_project_rollback_start_request();

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TRIGGER retain_project_rollback_start_request ON project_rollback_operations;
DROP FUNCTION retain_project_rollback_start_request();
DROP INDEX project_rollback_start_request_once;
ALTER TABLE project_rollback_operations
    DROP CONSTRAINT project_rollback_start_request_pair,
    DROP COLUMN start_request_id,
    DROP COLUMN start_request_options;
