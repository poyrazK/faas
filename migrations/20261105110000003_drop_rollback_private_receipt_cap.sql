-- migration-compat: expand
-- The public projection check was added and validated in separate transactions.
-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE deployment_rollback_operations
 DROP CONSTRAINT IF EXISTS deployment_rollback_operations_receipt_check;

-- +goose Down
SET LOCAL lock_timeout = '5s';
ALTER TABLE deployment_rollback_operations
 ADD CONSTRAINT deployment_rollback_operations_receipt_check CHECK (
  jsonb_typeof(receipt)='object' AND octet_length(receipt::text)<=16384
 ) NOT VALID;
