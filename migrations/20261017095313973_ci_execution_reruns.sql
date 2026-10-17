-- filename: 20261017095313973_ci_execution_reruns.sql

-- +goose Up
ALTER TABLE ci_executions ADD COLUMN IF NOT EXISTS rerun_parent_id uuid;
ALTER TABLE ci_executions ADD COLUMN IF NOT EXISTS rerun_root_id uuid;
-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='ci_executions'::regclass AND conname='ci_execution_rerun_parent_fk') THEN
  ALTER TABLE ci_executions ADD CONSTRAINT ci_execution_rerun_parent_fk FOREIGN KEY(account_id,rerun_parent_id)
   REFERENCES ci_executions(account_id,id) ON DELETE CASCADE;
 END IF;
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='ci_executions'::regclass AND conname='ci_execution_rerun_root_fk') THEN
  ALTER TABLE ci_executions ADD CONSTRAINT ci_execution_rerun_root_fk FOREIGN KEY(account_id,rerun_root_id)
   REFERENCES ci_executions(account_id,id) ON DELETE CASCADE;
 END IF;
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='ci_executions'::regclass AND conname='ci_execution_rerun_identity') THEN
  ALTER TABLE ci_executions ADD CONSTRAINT ci_execution_rerun_identity CHECK(
   (rerun_parent_id IS NULL AND rerun_root_id IS NULL AND (snapshot->'rerun' IS NULL OR snapshot->'rerun'='null'::jsonb)) OR
   (rerun_parent_id IS NOT NULL AND rerun_root_id IS NOT NULL AND mode='jobs'
    AND id<>rerun_parent_id AND id<>rerun_root_id AND jsonb_typeof(snapshot->'rerun')='object'
    AND snapshot->'rerun'->>'parent_execution_id' IS NOT NULL AND snapshot->'rerun'->>'root_execution_id' IS NOT NULL
    AND snapshot->'rerun'->>'parent_execution_id'=rerun_parent_id::text AND snapshot->'rerun'->>'root_execution_id'=rerun_root_id::text));
 END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM ci_executions WHERE rerun_parent_id IS NOT NULL OR snapshot->'rerun' IS NOT NULL)
 OR EXISTS(SELECT 1 FROM ci_execution_archives WHERE receipt->'rerun' IS NOT NULL) THEN
  RAISE EXCEPTION 'retain CI execution rerun lineage and idempotency evidence before rollback';
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE ci_executions DROP CONSTRAINT ci_execution_rerun_identity;
ALTER TABLE ci_executions DROP CONSTRAINT ci_execution_rerun_root_fk;
ALTER TABLE ci_executions DROP CONSTRAINT ci_execution_rerun_parent_fk;
ALTER TABLE ci_executions DROP COLUMN rerun_root_id;
ALTER TABLE ci_executions DROP COLUMN rerun_parent_id;
