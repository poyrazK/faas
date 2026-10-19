-- migration-compat: expand
-- ADR-1198: an operation webhook binding either starts Operations (the
-- ADR-1193 default) or delivers signals to waiting ones, matched by a
-- business subject read from the verified provider payload. The previous
-- release writes start-mode bindings only, which the defaults keep valid.
-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE customer_operation_webhook_bindings ADD COLUMN IF NOT EXISTS mode text NOT NULL DEFAULT 'start';
ALTER TABLE customer_operation_webhook_bindings ADD COLUMN IF NOT EXISTS subject_type text;
ALTER TABLE customer_operation_webhook_bindings ADD COLUMN IF NOT EXISTS subject_id_from text;
ALTER TABLE customer_operation_webhook_bindings ADD COLUMN IF NOT EXISTS signals jsonb NOT NULL DEFAULT '[]';
ALTER TABLE customer_operation_webhook_bindings DROP CONSTRAINT IF EXISTS customer_operation_webhook_bindings_mode_check;
ALTER TABLE customer_operation_webhook_bindings ADD CONSTRAINT customer_operation_webhook_bindings_mode_check CHECK (
    jsonb_typeof(signals) = 'array' AND jsonb_array_length(signals) <= 32 AND (
        (mode = 'start' AND subject_type IS NULL AND subject_id_from IS NULL AND jsonb_array_length(signals) = 0)
        OR (mode = 'signal' AND subject_type IS NOT NULL AND subject_id_from IS NOT NULL
            AND jsonb_array_length(signals) > 0 AND cardinality(event_types) = 0)));

-- +goose Down
ALTER TABLE customer_operation_webhook_bindings DROP CONSTRAINT IF EXISTS customer_operation_webhook_bindings_mode_check;
ALTER TABLE customer_operation_webhook_bindings DROP COLUMN IF EXISTS signals;
ALTER TABLE customer_operation_webhook_bindings DROP COLUMN IF EXISTS subject_id_from;
ALTER TABLE customer_operation_webhook_bindings DROP COLUMN IF EXISTS subject_type;
ALTER TABLE customer_operation_webhook_bindings DROP COLUMN IF EXISTS mode;
