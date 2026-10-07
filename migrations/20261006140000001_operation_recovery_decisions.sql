-- +goose Up
ALTER TABLE customer_operation_recoveries ADD COLUMN decision jsonb
 CHECK (decision IS NULL OR jsonb_typeof(decision)='object');

-- +goose Down
-- Forward-only: accepted immutable acknowledgements survive rollback.
SELECT 1;
