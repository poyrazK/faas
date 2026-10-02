-- +goose Up
ALTER TABLE commit_sources ADD COLUMN IF NOT EXISTS operation_policy text;
-- +goose StatementBegin
DO $$
BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='commit_sources'::regclass AND conname='commit_source_operation_policy_name') THEN
  ALTER TABLE commit_sources ADD CONSTRAINT commit_source_operation_policy_name
 CHECK (operation_policy IS NULL OR operation_policy ~ '^[a-z][a-z0-9-]{0,62}$');
 END IF;
END;
$$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $$
BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='commit_sources'::regclass AND conname='commit_source_operation_policy_owner') THEN
  ALTER TABLE commit_sources ADD CONSTRAINT commit_source_operation_policy_owner
 FOREIGN KEY(account_id,operation_policy) REFERENCES exclusive_work_policies(account_id,name);
 END IF;
END;
$$;
-- +goose StatementEnd

ALTER TABLE commit_receipts ALTER COLUMN invocation_id DROP NOT NULL;
ALTER TABLE commit_receipts ADD COLUMN IF NOT EXISTS operation_id uuid;
-- +goose StatementBegin
DO $$
BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='commit_receipts'::regclass AND conname='commit_receipt_one_operation') THEN
  ALTER TABLE commit_receipts ADD CONSTRAINT commit_receipt_one_operation
 CHECK ((invocation_id IS NOT NULL)::integer + (operation_id IS NOT NULL)::integer = 1);
 END IF;
END;
$$;
-- +goose StatementEnd
CREATE UNIQUE INDEX IF NOT EXISTS commit_receipts_managed_operation_identity ON commit_receipts(operation_id)
 WHERE operation_id IS NOT NULL;

-- Minimal retained facts survive operation/result retention. This trigger runs
-- in the same transaction as the managed Operations owner transition.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION record_commit_managed_operation_state() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 UPDATE commit_receipts SET operation_state=CASE NEW.state
 WHEN 'pending' THEN 'accepted' WHEN 'running' THEN 'running'
 WHEN 'completed' THEN 'completed' WHEN 'cancelled' THEN 'cancelled'
 WHEN 'failed' THEN 'failed' WHEN 'expired' THEN 'failed'
 ELSE 'unknown' END, completed_at=NEW.completed_at
 WHERE operation_id=NEW.id;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS commit_managed_operation_state ON exclusive_work_operations;
CREATE TRIGGER commit_managed_operation_state AFTER UPDATE OF state,completed_at
 ON exclusive_work_operations FOR EACH ROW EXECUTE FUNCTION record_commit_managed_operation_state();

-- +goose Down
-- Refuse to turn retained managed identities into legacy invocation identities.
-- +goose StatementBegin
DO $$
BEGIN
 IF EXISTS (SELECT 1 FROM commit_receipts WHERE operation_id IS NOT NULL)
 OR EXISTS (SELECT 1 FROM commit_sources WHERE operation_policy IS NOT NULL) THEN
  RAISE EXCEPTION 'managed Commit sources and receipts prevent this downgrade';
 END IF;
END;
$$;
-- +goose StatementEnd
DROP TRIGGER commit_managed_operation_state ON exclusive_work_operations;
DROP FUNCTION record_commit_managed_operation_state();
ALTER TABLE commit_receipts DROP CONSTRAINT commit_receipt_one_operation;
ALTER TABLE commit_receipts DROP COLUMN operation_id;
ALTER TABLE commit_receipts ALTER COLUMN invocation_id SET NOT NULL;
ALTER TABLE commit_sources DROP COLUMN operation_policy;
