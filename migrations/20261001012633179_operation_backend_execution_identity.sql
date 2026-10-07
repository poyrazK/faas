-- filename: 20261001012633179_operation_backend_execution_identity.sql

-- +goose Up
-- +goose StatementBegin
-- ADR-521. A logical generation binds a real backend execution, never a
-- synthetic HTTP invocation standing in for a workflow or a Job.
ALTER TABLE customer_operation_executions ALTER COLUMN invocation_id DROP NOT NULL;
ALTER TABLE customer_operation_executions ADD COLUMN IF NOT EXISTS workflow_run_id uuid REFERENCES workflow_runs(id) ON DELETE RESTRICT;
ALTER TABLE customer_operation_executions ADD COLUMN IF NOT EXISTS job_run_id uuid REFERENCES job_runs(id) ON DELETE RESTRICT;
ALTER TABLE customer_operation_executions ADD COLUMN IF NOT EXISTS execution_id uuid
 GENERATED ALWAYS AS (coalesce(invocation_id,workflow_run_id,job_run_id)) STORED;
ALTER TABLE customer_operation_executions ADD COLUMN IF NOT EXISTS execution_kind text
 GENERATED ALWAYS AS (CASE WHEN invocation_id IS NOT NULL THEN 'http' WHEN workflow_run_id IS NOT NULL THEN 'workflow' ELSE 'job' END) STORED;
ALTER TABLE customer_operation_executions ALTER COLUMN execution_id SET NOT NULL;
ALTER TABLE customer_operation_executions ALTER COLUMN execution_kind SET NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS customer_operation_executions_identity_idx ON customer_operation_executions(execution_id);
CREATE UNIQUE INDEX IF NOT EXISTS customer_operation_executions_owner_idx ON customer_operation_executions(operation_id,execution_id);
ALTER TABLE customer_operations DROP CONSTRAINT IF EXISTS customer_operations_current_execution_fkey;
-- Later workflow-custody migrations reference this unique index. Detach and
-- restore that FK around replay so the execution-identity migration stays
-- idempotent after dependent ledgers have been installed.
ALTER TABLE IF EXISTS customer_operation_workflow_claims
 DROP CONSTRAINT IF EXISTS customer_operation_workflow_claims_execution_identity_fkey;
DROP INDEX IF EXISTS customer_operation_executions_current_idx;
CREATE UNIQUE INDEX customer_operation_executions_current_idx ON customer_operation_executions(operation_id,generation,execution_id,execution_kind);
DO $$ BEGIN
 IF to_regclass('customer_operation_workflow_claims') IS NOT NULL
    AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid=to_regclass('customer_operation_workflow_claims') AND conname='customer_operation_workflow_claims_execution_identity_fkey') THEN
  ALTER TABLE customer_operation_workflow_claims ADD CONSTRAINT customer_operation_workflow_claims_execution_identity_fkey
   FOREIGN KEY(operation_id,generation,workflow_run_id,execution_kind)
   REFERENCES customer_operation_executions(operation_id,generation,execution_id,execution_kind) ON DELETE CASCADE;
 END IF;
END $$;

-- Generated columns preserve earlier HTTP writers: their existing invocation
-- field remains the fallback when the new JSON fields are absent.
ALTER TABLE customer_operations ALTER COLUMN current_invocation_id DROP NOT NULL;
ALTER TABLE customer_operations ADD COLUMN IF NOT EXISTS current_execution_id uuid
 GENERATED ALWAYS AS (coalesce((record->>'current_execution_id')::uuid,current_invocation_id)) STORED;
ALTER TABLE customer_operations ADD COLUMN IF NOT EXISTS execution_kind text
 GENERATED ALWAYS AS (coalesce(record->>'execution_kind','http')) STORED;
ALTER TABLE customer_operations ADD COLUMN IF NOT EXISTS execution_generation integer
 GENERATED ALWAYS AS ((record->>'generation')::integer) STORED;
ALTER TABLE customer_operations ALTER COLUMN current_execution_id SET NOT NULL;
ALTER TABLE customer_operations ALTER COLUMN execution_kind SET NOT NULL;
ALTER TABLE customer_operations ALTER COLUMN execution_generation SET NOT NULL;

-- These markers survive projection GC. Legacy replay paths must not restart
-- operation-owned work after its customer projection has expired.
ALTER TABLE workflow_runs ADD COLUMN IF NOT EXISTS operation_id uuid;
ALTER TABLE job_runs ADD COLUMN IF NOT EXISTS operation_id uuid;
CREATE INDEX IF NOT EXISTS customer_operation_idempotency_operation_idx ON customer_operation_idempotency(operation_id);

DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='customer_operation_executions'::regclass AND conname='customer_operation_executions_backend_check') THEN
  ALTER TABLE customer_operation_executions ADD CONSTRAINT customer_operation_executions_backend_check
   CHECK (num_nonnulls(invocation_id,workflow_run_id,job_run_id)=1 AND execution_kind IN ('http','workflow','job'));
 END IF;
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='customer_operations'::regclass AND conname='customer_operations_backend_check') THEN
  ALTER TABLE customer_operations ADD CONSTRAINT customer_operations_backend_check
   CHECK (execution_kind IN ('http','workflow','job') AND
    ((execution_kind='http' AND current_invocation_id IS NOT NULL AND current_execution_id=current_invocation_id)
     OR (execution_kind IN ('workflow','job') AND current_invocation_id IS NULL)));
 END IF;
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='customer_operations'::regclass AND conname='customer_operations_current_execution_fkey') THEN
  ALTER TABLE customer_operations ADD CONSTRAINT customer_operations_current_execution_fkey
   FOREIGN KEY (id,execution_generation,current_execution_id,execution_kind)
   REFERENCES customer_operation_executions(operation_id,generation,execution_id,execution_kind)
   DEFERRABLE INITIALLY DEFERRED;
 END IF;
END $$;

-- Event/report identity is scoped to its operation and can reference each
-- backend. Deferral also permits operation deletion to cascade both ledgers.
ALTER TABLE customer_operation_events DROP CONSTRAINT IF EXISTS customer_operation_events_execution_id_fkey;
ALTER TABLE customer_operation_reports DROP CONSTRAINT IF EXISTS customer_operation_reports_execution_id_fkey;
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='customer_operation_events'::regclass AND conname='customer_operation_events_execution_owner_fkey') THEN
  ALTER TABLE customer_operation_events ADD CONSTRAINT customer_operation_events_execution_owner_fkey
   FOREIGN KEY (operation_id,execution_id) REFERENCES customer_operation_executions(operation_id,execution_id)
   DEFERRABLE INITIALLY DEFERRED;
 END IF;
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='customer_operation_reports'::regclass AND conname='customer_operation_reports_execution_owner_fkey') THEN
  ALTER TABLE customer_operation_reports ADD CONSTRAINT customer_operation_reports_execution_owner_fkey
   FOREIGN KEY (operation_id,execution_id) REFERENCES customer_operation_executions(operation_id,execution_id)
   DEFERRABLE INITIALLY DEFERRED;
 END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- Forward-only: preserve operation/execution history and permanent replay fences.
SELECT 1;
