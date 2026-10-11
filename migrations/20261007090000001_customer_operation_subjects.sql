-- filename: 20261007090000001_customer_operation_subjects.sql
-- ADR-714: immutable, application-selected business correlation metadata.
-- Replay-safety: guard the named CHECK and trigger, replace the function,
-- and use IF NOT EXISTS for the two indexes.

-- +goose Up
-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_catalog.pg_constraint
        WHERE conname = 'customer_operation_subject_valid'
          AND conrelid = 'customer_operations'::regclass
    ) THEN
        ALTER TABLE customer_operations ADD CONSTRAINT customer_operation_subject_valid CHECK (
            NOT (record ? 'subject') OR coalesce(
                jsonb_typeof(record->'subject') = 'object'
                AND ((record->'subject') - 'type' - 'id') = '{}'::jsonb
                AND jsonb_typeof(record->'subject'->'type') = 'string'
                AND (record->'subject'->>'type') ~ '^[a-z][a-z0-9-]{0,63}$'
                AND jsonb_typeof(record->'subject'->'id') = 'string'
                AND octet_length(record->'subject'->>'id') BETWEEN 1 AND 256
                AND (record->'subject'->>'id') !~ E'[\\x01-\\x1f\\x7f]', false)
        );
    END IF;
END$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION customer_operation_subject_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.record->'subject' IS DISTINCT FROM NEW.record->'subject' THEN
        RAISE EXCEPTION 'customer operation subject is immutable'
            USING ERRCODE = '23514', CONSTRAINT = 'customer_operation_subject_immutable';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_catalog.pg_trigger
        WHERE tgname = 'customer_operation_subject_immutable'
          AND tgrelid = 'customer_operations'::regclass
          AND NOT tgisinternal
    ) THEN
        CREATE TRIGGER customer_operation_subject_immutable
            BEFORE UPDATE OF record ON customer_operations
            FOR EACH ROW EXECUTE FUNCTION customer_operation_subject_immutable();
    END IF;
END$$;
-- +goose StatementEnd

CREATE INDEX IF NOT EXISTS customer_operations_tenant_subject_history_idx ON customer_operations
    (account_id, app_id, platform_tenant_id, (record #>> '{subject,type}'), (record #>> '{subject,id}'), created_at DESC, id DESC)
    WHERE record ? 'subject';
CREATE INDEX IF NOT EXISTS customer_operations_account_subject_history_idx ON customer_operations
    (account_id, app_id, (record #>> '{subject,type}'), (record #>> '{subject,id}'), created_at DESC, id DESC)
    WHERE record ? 'subject';

-- +goose Down
DROP INDEX IF EXISTS customer_operations_account_subject_history_idx;
DROP INDEX IF EXISTS customer_operations_tenant_subject_history_idx;
DROP TRIGGER IF EXISTS customer_operation_subject_immutable ON customer_operations;
DROP FUNCTION IF EXISTS customer_operation_subject_immutable();
ALTER TABLE customer_operations DROP CONSTRAINT IF EXISTS customer_operation_subject_valid;
