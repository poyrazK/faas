-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE deployment_rollback_operations
 VALIDATE CONSTRAINT deployment_rollback_operations_public_receipt_check;

-- +goose Down
SELECT 1;
