-- filename: 20261016213037860_managed_data_repair_operations.sql

-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
-- A repair is a native execution, with no synthetic invocation or customer
-- handler definition. Existing execution writers keep deriving their identity.
ALTER TABLE customer_operation_executions ADD COLUMN IF NOT EXISTS data_repair_id uuid;
ALTER TABLE customer_operation_executions ALTER COLUMN execution_id DROP EXPRESSION IF EXISTS;
ALTER TABLE customer_operation_executions ALTER COLUMN execution_kind DROP EXPRESSION IF EXISTS;
CREATE OR REPLACE FUNCTION customer_operation_backend_identity() RETURNS trigger
LANGUAGE plpgsql AS $$ BEGIN
 NEW.execution_id := coalesce(NEW.invocation_id, NEW.workflow_run_id, NEW.job_run_id, NEW.data_repair_id);
 NEW.execution_kind := CASE WHEN NEW.invocation_id IS NOT NULL THEN 'http'
   WHEN NEW.workflow_run_id IS NOT NULL THEN 'workflow'
   WHEN NEW.job_run_id IS NOT NULL THEN 'job' ELSE 'data_repair' END;
 RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS customer_operation_backend_identity ON customer_operation_executions;
CREATE TRIGGER customer_operation_backend_identity BEFORE INSERT OR UPDATE
 ON customer_operation_executions FOR EACH ROW EXECUTE FUNCTION customer_operation_backend_identity();
ALTER TABLE customer_operation_executions DROP CONSTRAINT IF EXISTS customer_operation_executions_backend_check;
ALTER TABLE customer_operation_executions ADD CONSTRAINT customer_operation_executions_backend_check
 CHECK (num_nonnulls(invocation_id,workflow_run_id,job_run_id,data_repair_id)=1
 AND execution_id=coalesce(invocation_id,workflow_run_id,job_run_id,data_repair_id)
 AND execution_kind=CASE WHEN invocation_id IS NOT NULL THEN 'http' WHEN workflow_run_id IS NOT NULL THEN 'workflow' WHEN job_run_id IS NOT NULL THEN 'job' ELSE 'data_repair' END
 AND (data_repair_id IS NULL OR data_repair_id=operation_id)
 AND execution_kind IN ('http','workflow','job','data_repair'));

ALTER TABLE customer_operations ADD COLUMN IF NOT EXISTS data_repair_id uuid;
ALTER TABLE customer_operations ALTER COLUMN definition_id DROP NOT NULL;
ALTER TABLE customer_operations DROP CONSTRAINT IF EXISTS customer_operations_backend_check;
ALTER TABLE customer_operations DROP CONSTRAINT IF EXISTS customer_operations_execution_family_check;
ALTER TABLE customer_operations ADD CONSTRAINT customer_operations_execution_family_check
 CHECK (num_nonnulls(current_invocation_id,workflow_run_id,job_run_id,data_repair_id)=1);
ALTER TABLE customer_operations ADD CONSTRAINT customer_operations_backend_check CHECK (coalesce((
 (execution_kind='http' AND current_execution_id=current_invocation_id)
 OR (execution_kind='workflow' AND current_execution_id=workflow_run_id)
 OR (execution_kind='job' AND current_execution_id=job_run_id)
 OR (execution_kind='data_repair' AND current_execution_id=data_repair_id AND data_repair_id=id)),false));
ALTER TABLE customer_operations DROP CONSTRAINT IF EXISTS customer_operations_data_repair_check;
ALTER TABLE customer_operations ADD CONSTRAINT customer_operations_data_repair_check CHECK (coalesce((
 (data_repair_id IS NULL AND definition_id IS NOT NULL AND NOT(record ? 'data_repair'))
 OR (data_repair_id IS NOT NULL AND definition_id IS NULL
 AND jsonb_typeof(record->'data_repair')='object'
 AND record->>'data_repair_id'=data_repair_id::text)),false));
-- +goose StatementEnd

-- +goose Down
-- Forward-only: preserve admitted repairs, approvals and commit recovery.
SELECT 1;
