-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';

-- A worker from an older release may rewrite the receipt without plan_hash.
-- +goose StatementBegin
CREATE FUNCTION retain_project_rollback_plan_hash() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.receipt ? 'plan_hash' THEN
        NEW.receipt := NEW.receipt || jsonb_build_object('plan_hash', OLD.receipt->'plan_hash');
    END IF;
    RETURN NEW;
END $$;
-- +goose StatementEnd

CREATE TRIGGER retain_project_rollback_plan_hash
    BEFORE UPDATE OF receipt ON project_rollback_operations
    FOR EACH ROW EXECUTE FUNCTION retain_project_rollback_plan_hash();

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TRIGGER retain_project_rollback_plan_hash ON project_rollback_operations;
DROP FUNCTION retain_project_rollback_plan_hash();
