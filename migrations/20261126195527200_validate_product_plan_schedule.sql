-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE product_control_customer_changes VALIDATE CONSTRAINT product_plan_schedule_status_chk;

-- +goose Down
SELECT 1;
