-- +goose Up
ALTER TABLE object_storage_write_admissions ADD COLUMN IF NOT EXISTS route_receipt boolean NOT NULL DEFAULT false;
-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='object_storage_write_admissions'::regclass AND conname='object_write_route_proxy') THEN
  ALTER TABLE object_storage_write_admissions ADD CONSTRAINT object_write_route_proxy CHECK (NOT route_receipt OR kind='proxy');
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE object_upload_completions ADD COLUMN IF NOT EXISTS write_phase text NOT NULL DEFAULT 'untracked' CHECK (write_phase IN ('untracked','prepared','dispatched','settled'));
ALTER TABLE object_upload_completions ADD COLUMN IF NOT EXISTS recovery_token text NOT NULL DEFAULT '';
ALTER TABLE object_upload_completions ADD COLUMN IF NOT EXISTS recovery_lease_until timestamptz;
ALTER TABLE object_upload_completions ADD COLUMN IF NOT EXISTS recovery_retry_at timestamptz NOT NULL DEFAULT now();
-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='object_upload_completions'::regclass AND conname='object_upload_tracked_status') THEN
  ALTER TABLE object_upload_completions ADD CONSTRAINT object_upload_tracked_status CHECK (write_phase='untracked' OR ((write_phase='settled')=(status IN ('completed','failed')) AND status<>'rejected'));
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='object_upload_completions'::regclass AND conname='object_upload_recovery_lease') THEN
  ALTER TABLE object_upload_completions ADD CONSTRAINT object_upload_recovery_lease CHECK ((recovery_token='')=(recovery_lease_until IS NULL));
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='object_upload_completions'::regclass AND conname='object_upload_recovery_pending') THEN
  ALTER TABLE object_upload_completions ADD CONSTRAINT object_upload_recovery_pending CHECK (recovery_lease_until IS NULL OR write_phase='dispatched');
 END IF;
END $$;
-- +goose StatementEnd
-- Preserve recovery records when a route is deleted. Bucket/account deletion still cascades.
ALTER TABLE object_upload_completions DROP CONSTRAINT IF EXISTS object_upload_completions_route_id_fkey;
ALTER TABLE object_upload_completions ALTER COLUMN route_id DROP NOT NULL;
-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='object_upload_completions'::regclass AND conname='object_upload_completions_route_id_fkey') THEN
  ALTER TABLE object_upload_completions ADD CONSTRAINT object_upload_completions_route_id_fkey FOREIGN KEY (route_id) REFERENCES object_upload_routes(id) ON DELETE SET NULL;
 END IF;
END $$;
-- +goose StatementEnd
CREATE INDEX IF NOT EXISTS object_upload_recovery_due_idx ON object_upload_completions(recovery_retry_at,id) WHERE write_phase IN ('prepared','dispatched');

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM object_upload_completions WHERE write_phase IN ('prepared','dispatched')) OR EXISTS (SELECT 1 FROM object_storage_write_admissions WHERE route_receipt AND state='pending') THEN
  RAISE EXCEPTION 'Settle tracked upload intents before rollback';
 END IF;
END $$;
-- +goose StatementEnd
-- Rollback/reapply cannot silently make route grants reclaimable for old replicas.
UPDATE object_storage_key_grants g SET reclaimable=false,last_write_id=NULL
 WHERE EXISTS (SELECT 1 FROM object_storage_write_admissions w WHERE w.id=g.last_write_id AND w.route_receipt);
DELETE FROM object_storage_write_admissions WHERE route_receipt;
DROP INDEX object_upload_recovery_due_idx;
ALTER TABLE object_upload_completions
 DROP CONSTRAINT object_upload_tracked_status, DROP CONSTRAINT object_upload_recovery_lease, DROP CONSTRAINT object_upload_recovery_pending,
 DROP COLUMN write_phase, DROP COLUMN recovery_token, DROP COLUMN recovery_lease_until, DROP COLUMN recovery_retry_at;
DELETE FROM object_upload_completions WHERE route_id IS NULL;
ALTER TABLE object_upload_completions DROP CONSTRAINT object_upload_completions_route_id_fkey;
ALTER TABLE object_upload_completions ALTER COLUMN route_id SET NOT NULL;
ALTER TABLE object_upload_completions ADD CONSTRAINT object_upload_completions_route_id_fkey FOREIGN KEY (route_id) REFERENCES object_upload_routes(id) ON DELETE CASCADE;
ALTER TABLE object_storage_write_admissions DROP CONSTRAINT object_write_route_proxy, DROP COLUMN route_receipt;
