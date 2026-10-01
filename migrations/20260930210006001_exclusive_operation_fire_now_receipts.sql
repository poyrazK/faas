-- filename: 20260930210006001_exclusive_operation_fire_now_receipts.sql

-- +goose Up
ALTER TABLE cron_fire_now_requests
  ADD COLUMN IF NOT EXISTS operation_id uuid REFERENCES exclusive_work_operations(id) ON DELETE SET NULL;

-- +goose Down
ALTER TABLE cron_fire_now_requests DROP COLUMN operation_id;
