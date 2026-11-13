-- migration-compat: expand
-- Exact migration acknowledgements are internal evidence, not public receipt
-- fields. Keep their existing representation readable by N-1 control planes.
-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
DO $$
BEGIN
IF NOT EXISTS (SELECT 1 FROM pg_constraint
 WHERE conrelid='deployment_rollback_operations'::regclass
 AND conname='deployment_rollback_operations_public_receipt_check') THEN
ALTER TABLE deployment_rollback_operations
 ADD CONSTRAINT deployment_rollback_operations_public_receipt_check CHECK (
  jsonb_typeof(receipt)='object'
  AND octet_length((receipt-'_release_review')::text)<=16384
  AND (NOT receipt ? '_release_review' OR jsonb_typeof(receipt->'_release_review') IN ('array','null'))
 ) NOT VALID;
END IF;
END;
$$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
ALTER TABLE deployment_rollback_operations
 DROP CONSTRAINT deployment_rollback_operations_public_receipt_check;
