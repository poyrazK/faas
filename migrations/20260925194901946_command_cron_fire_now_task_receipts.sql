-- filename: 20260925194901946_command_cron_fire_now_task_receipts.sql

-- +goose Up
-- +goose StatementBegin
-- Link command-cron fire-now requests to their deployment-attached app task.
-- The request and task are created atomically by the scheduler so polling
-- returns a durable receipt without advancing the cron schedule cursor.
ALTER TABLE cron_fire_now_requests
    ADD COLUMN IF NOT EXISTS task_id uuid NULL REFERENCES app_tasks(id) ON DELETE SET NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE cron_fire_now_requests DROP COLUMN IF EXISTS task_id;
-- +goose StatementEnd
