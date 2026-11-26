-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';

ALTER TABLE product_control_customer_changes
    ADD COLUMN scheduled_for timestamptz,
    ADD COLUMN schedule_status text,
    ADD CONSTRAINT product_plan_schedule_status_chk CHECK (
        (scheduled_for IS NULL AND schedule_status IS NULL) OR
        (scheduled_for IS NOT NULL AND schedule_status IS NOT NULL AND
         schedule_status IN ('pending', 'applied', 'cancelled', 'needs_review'))
    ) NOT VALID;

-- +goose StatementBegin
CREATE FUNCTION invalidate_product_plan_schedule() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    UPDATE product_control_customer_changes SET schedule_status = 'needs_review'
    WHERE account_id = OLD.account_id AND app_id = OLD.app_id AND tenant_id = OLD.tenant_id
      AND schedule_status = 'pending';
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER product_plan_schedule_on_customer_change
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
