-- migration-compat: expand
-- ADR-20261016100929: an operation webhook binding either starts Operations (the
-- ADR-1193 default) or delivers signals to waiting ones, matched by a
-- business subject read from the verified provider payload. The previous
-- release writes start-mode bindings only, which the defaults keep valid.
-- Each statement is its own short transaction (NO TRANSACTION): no step
-- locks more than three existing tables or scans one while blocking writes,
-- constraints are added NOT VALID and validated separately, and every step
-- is idempotent, so an interrupted run resumes.
-- +goose NO TRANSACTION
-- +goose Up
SET lock_timeout = '5s';
ALTER TABLE customer_operation_webhook_bindings ADD COLUMN IF NOT EXISTS mode text NOT NULL DEFAULT 'start';
ALTER TABLE customer_operation_webhook_bindings ADD COLUMN IF NOT EXISTS subject_type text;
ALTER TABLE customer_operation_webhook_bindings ADD COLUMN IF NOT EXISTS subject_id_from text;
ALTER TABLE customer_operation_webhook_bindings ADD COLUMN IF NOT EXISTS signals jsonb NOT NULL DEFAULT '[]';
-- +goose StatementBegin
DO $guard$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'customer_operation_webhook_bindings_mode_check'
                   AND conrelid = 'customer_operation_webhook_bindings'::regclass) THEN
        ALTER TABLE customer_operation_webhook_bindings ADD CONSTRAINT customer_operation_webhook_bindings_mode_check CHECK (
    jsonb_typeof(signals) = 'array' AND jsonb_array_length(signals) <= 32 AND (
        (mode = 'start' AND subject_type IS NULL AND subject_id_from IS NULL AND jsonb_array_length(signals) = 0)
        OR (mode = 'signal' AND subject_type IS NOT NULL AND subject_id_from IS NOT NULL
            AND jsonb_array_length(signals) > 0 AND cardinality(event_types) = 0))) NOT VALID;
    END IF;
END
$guard$;
-- +goose StatementEnd
ALTER TABLE customer_operation_webhook_bindings VALIDATE CONSTRAINT customer_operation_webhook_bindings_mode_check;
RESET lock_timeout;

-- +goose Down
SET lock_timeout = '5s';
ALTER TABLE customer_operation_webhook_bindings DROP CONSTRAINT IF EXISTS customer_operation_webhook_bindings_mode_check;
ALTER TABLE customer_operation_webhook_bindings DROP COLUMN IF EXISTS signals;
ALTER TABLE customer_operation_webhook_bindings DROP COLUMN IF EXISTS subject_id_from;
ALTER TABLE customer_operation_webhook_bindings DROP COLUMN IF EXISTS subject_type;
ALTER TABLE customer_operation_webhook_bindings DROP COLUMN IF EXISTS mode;
RESET lock_timeout;
