-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';

ALTER TABLE product_control_customer_changes
    ADD COLUMN IF NOT EXISTS scheduled_for timestamptz,
    ADD COLUMN IF NOT EXISTS schedule_status text;

-- +goose StatementBegin
DO $$ BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'product_control_customer_changes'::regclass
          AND conname = 'product_plan_schedule_status_chk'
    ) THEN
        ALTER TABLE product_control_customer_changes
            ADD CONSTRAINT product_plan_schedule_status_chk CHECK (
                (scheduled_for IS NULL AND schedule_status IS NULL) OR
                (scheduled_for IS NOT NULL AND schedule_status IS NOT NULL AND
                 schedule_status IN ('pending', 'applied', 'cancelled', 'needs_review'))
            ) NOT VALID;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION invalidate_product_plan_schedule() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    UPDATE product_control_customer_changes SET schedule_status = 'needs_review'
    WHERE account_id = OLD.account_id AND app_id = OLD.app_id AND tenant_id = OLD.tenant_id
      AND schedule_status = 'pending';
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE OR REPLACE TRIGGER product_plan_schedule_on_customer_change
    AFTER UPDATE OF plan, revision, suspended, auth_organization_id ON product_control_customers
    FOR EACH ROW WHEN (ROW(OLD.plan, OLD.revision, OLD.suspended, OLD.auth_organization_id)
                       IS DISTINCT FROM ROW(NEW.plan, NEW.revision, NEW.suspended, NEW.auth_organization_id))
    EXECUTE FUNCTION invalidate_product_plan_schedule();

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TRIGGER product_plan_schedule_on_customer_change ON product_control_customers;
DROP FUNCTION invalidate_product_plan_schedule();
ALTER TABLE product_control_customer_changes
    DROP CONSTRAINT product_plan_schedule_status_chk,
    DROP COLUMN schedule_status,
    DROP COLUMN scheduled_for;
