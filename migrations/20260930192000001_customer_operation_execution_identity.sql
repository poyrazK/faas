-- +goose Up
-- +goose StatementBegin
-- Preserve the recovery boundary after the bulky operation projection expires.
-- There is deliberately no operation FK: invocation retention is independent.
ALTER TABLE invocations ADD COLUMN IF NOT EXISTS operation_id uuid;
UPDATE invocations i SET operation_id=e.operation_id FROM customer_operation_executions e WHERE e.invocation_id=i.id AND i.operation_id IS NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
