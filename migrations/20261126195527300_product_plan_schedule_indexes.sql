-- migration-compat: expand
-- +goose NO TRANSACTION
-- +goose Up
SET lock_timeout = '5s';
SET statement_timeout = 0;

-- +goose StatementBegin
DO $$ BEGIN
    -- A failed concurrent build leaves an invalid index. Rebuild it on retry.
    IF EXISTS (SELECT 1 FROM pg_index WHERE indexrelid = to_regclass('product_plan_schedule_pending_idx') AND NOT indisvalid) THEN
        EXECUTE 'DROP INDEX product_plan_schedule_pending_idx';
    END IF;
    IF EXISTS (SELECT 1 FROM pg_index WHERE indexrelid = to_regclass('product_plan_schedule_due_idx') AND NOT indisvalid) THEN
        EXECUTE 'DROP INDEX product_plan_schedule_due_idx';
    END IF;
    IF EXISTS (SELECT 1 FROM pg_index WHERE indexrelid = to_regclass('product_plan_schedule_customer_idx') AND NOT indisvalid) THEN
        EXECUTE 'DROP INDEX product_plan_schedule_customer_idx';
    END IF;
END $$;
-- +goose StatementEnd

CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS product_plan_schedule_pending_idx
    ON product_control_customer_changes (account_id, app_id, tenant_id)
    WHERE schedule_status = 'pending';
CREATE INDEX CONCURRENTLY IF NOT EXISTS product_plan_schedule_due_idx
    ON product_control_customer_changes (scheduled_for, id)
    WHERE schedule_status = 'pending';
CREATE INDEX CONCURRENTLY IF NOT EXISTS product_plan_schedule_customer_idx
    ON product_control_customer_changes (account_id, app_id, tenant_id, created_at DESC, id DESC)
    WHERE scheduled_for IS NOT NULL;

RESET lock_timeout;
RESET statement_timeout;

-- +goose Down
SET lock_timeout = '5s';
DROP INDEX CONCURRENTLY IF EXISTS product_plan_schedule_customer_idx;
DROP INDEX CONCURRENTLY IF EXISTS product_plan_schedule_due_idx;
DROP INDEX CONCURRENTLY IF EXISTS product_plan_schedule_pending_idx;
RESET lock_timeout;
