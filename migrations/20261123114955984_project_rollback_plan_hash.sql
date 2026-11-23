-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';

-- A worker from an older release may rewrite the receipt without plan_hash.
-- Keep the resume-plan guard when replaying this earlier migration.
-- +goose StatementBegin
DO $migration$
BEGIN
    IF to_regprocedure(format('%I.retain_project_rollback_plan_hash()', current_schema())) IS NULL THEN
        CREATE FUNCTION retain_project_rollback_plan_hash() RETURNS trigger LANGUAGE plpgsql AS $$
        BEGIN
            IF OLD.receipt ? 'plan_hash' THEN
                NEW.receipt := NEW.receipt || jsonb_build_object('plan_hash', OLD.receipt->'plan_hash');
            END IF;
            RETURN NEW;
        END $$;
    END IF;
END
$migration$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_trigger
        WHERE tgrelid = 'project_rollback_operations'::regclass AND tgname = 'retain_project_rollback_plan_hash'
    ) THEN
        CREATE TRIGGER retain_project_rollback_plan_hash
            BEFORE UPDATE OF receipt ON project_rollback_operations
            FOR EACH ROW EXECUTE FUNCTION retain_project_rollback_plan_hash();
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TRIGGER retain_project_rollback_plan_hash ON project_rollback_operations;
DROP FUNCTION retain_project_rollback_plan_hash();
