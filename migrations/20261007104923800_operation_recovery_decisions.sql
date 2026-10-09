-- +goose Up
-- +goose StatementBegin
ALTER TABLE customer_operation_recoveries ADD COLUMN IF NOT EXISTS decision jsonb;
ALTER TABLE customer_operation_recoveries DROP CONSTRAINT IF EXISTS customer_operation_recoveries_decision_check;
ALTER TABLE customer_operation_recoveries ADD CONSTRAINT customer_operation_recoveries_decision_check
 CHECK (decision IS NULL OR jsonb_typeof(decision)='object');
-- +goose StatementEnd

-- +goose Down
-- Forward-only: accepted immutable acknowledgements survive rollback.
SELECT 1;
